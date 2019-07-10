package blobs_reflector

import (
	"encoding/json"
	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/reflector.go/cmd"
	"github.com/lbryio/reflector.go/db"
	"github.com/lbryio/reflector.go/reflector"
	"github.com/lbryio/reflector.go/store"
	"github.com/mitchellh/go-ps"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

func ReflectAndClean() error {
	err := reflectBlobs()
	if err != nil {
		return err
	}
	return cleanupLbrynet()
}

func loadConfig(path string) (cmd.Config, error) {
	var c cmd.Config

	raw, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, errors.Err("config file not found")
		}
		return c, err
	}

	err = json.Unmarshal(raw, &c)
	return c, err
}

func reflectBlobs() error {
	//make sure lbrynet is off
	running, err := isLbrynetRunning()
	if err != nil {
		return err
	}
	if running {
		return errors.Prefix("cannot reflect blobs as the daemon is running", err)
	}

	dbHandle := new(db.SQL)
	ex, err := os.Executable()
	if err != nil {
		return errors.Err(err)
	}
	exPath := filepath.Dir(ex)
	config, err := loadConfig(exPath + "/prismconfig.json")
	if err != nil {
		return errors.Err(err)
	}
	err = dbHandle.Connect(config.DBConn)
	if err != nil {
		return errors.Err(err)
	}
	st := store.NewDBBackedS3Store(
		store.NewS3BlobStore(config.AwsID, config.AwsSecret, config.BucketRegion, config.BucketName),
		dbHandle)

	uploadWorkers := 10
	uploader := reflector.NewUploader(dbHandle, st, uploadWorkers, false)
	usr, err := user.Current()
	if err != nil {
		return errors.Err(err)
	}
	blobsDir := usr.HomeDir + "/.lbrynet/blobfiles/"
	err = uploader.Upload(blobsDir)
	if err != nil {
		return errors.Err(err)
	}
	if uploader.GetSummary().Err > 0 {
		return errors.Err("not al blobs were reflected. Errors: %d", uploader.GetSummary().Err)
	}
	return nil
}

func cleanupLbrynet() error {
	//make sure lbrynet is off
	running, err := isLbrynetRunning()
	if err != nil {
		return err
	}
	if running {
		return errors.Prefix("cannot cleanup lbrynet as the daemon is running", err)
	}
	usr, err := user.Current()
	if err != nil {
		log.Errorln(err.Error())
		return errors.Err(err)
	}
	lbrynetDir := usr.HomeDir + "/.lbrynet/"
	files, err := filepath.Glob(lbrynetDir + "lbrynet.sqlite*")
	if err != nil {
		return errors.Err(err)
	}
	for _, f := range files {
		err = os.Remove(f)
		if err != nil {
			return errors.Err(err)
		}
	}
	err = os.RemoveAll(lbrynetDir + "/blobfiles/")
	if err != nil {
		return errors.Err(err)
	}
	return nil
}

func isLbrynetRunning() (bool, error) {
	processes, err := ps.Processes()
	if err != nil {
		return true, errors.Err(err)
	}
	var daemonProcessId = -1
	for _, p := range processes {
		if p.Executable() == "lbrynet" {
			daemonProcessId = p.Pid()
			break
		}
	}

	running := daemonProcessId != -1
	return running, nil
}
