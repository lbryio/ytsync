package blobs_reflector

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/reflector.go/cmd"
	"github.com/lbryio/reflector.go/db"
	"github.com/lbryio/reflector.go/reflector"
	"github.com/lbryio/reflector.go/store"
	"github.com/lbryio/ytsync/util"

	log "github.com/sirupsen/logrus"
)

func ReflectAndClean() error {
	err := reflectBlobs()
	if err != nil {
		return err
	}
	return util.CleanupLbrynet()
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
	if util.IsBlobReflectionOff() {
		return nil
	}
	//make sure lbrynet is off
	running, err := util.IsLbrynetRunning()
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
	config, err := loadConfig(exPath + "/prism_config.json")
	if err != nil {
		return errors.Err(err)
	}
	err = dbHandle.Connect(config.DBConn)
	if err != nil {
		return errors.Err(err)
	}
	defer func() {
		err := dbHandle.CloseDB()
		if err != nil {
			log.Errorf("failed to close db handle: %s", err.Error())
		}

	}()
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
