package util

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/lbrycrd"
	"github.com/lbryio/ytsync/v5/timing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/mitchellh/go-ps"
	log "github.com/sirupsen/logrus"
)

func GetBlobsDir() string {
	blobsDir := os.Getenv("BLOBS_DIRECTORY")
	if blobsDir == "" {
		usr, err := user.Current()
		if err != nil {
			log.Error(err.Error())
			return ""
		}
		blobsDir = usr.HomeDir + "/.lbrynet/blobfiles/"
	}

	return blobsDir
}

func IsBlobReflectionOff() bool {
	return os.Getenv("REFLECT_BLOBS") == "false"
}

func GetLBRYNetDir() string {
	lbrynetDir := os.Getenv("LBRYNET_DIR")
	if lbrynetDir == "" {
		usr, err := user.Current()
		if err != nil {
			log.Errorln(err.Error())
			return ""
		}
		return usr.HomeDir + "/.lbrynet/"
	}
	return lbrynetDir
}

func GetLbryumDir() string {
	lbryumDir := os.Getenv("LBRYUM_DIR")
	if lbryumDir == "" {
		usr, err := user.Current()
		if err != nil {
			log.Errorln(err.Error())
			return ""
		}
		return usr.HomeDir + "/.lbryum/"
	}
	return lbryumDir + "/"
}

const ALL = true
const ONLINE = false

func GetLBRYNetContainer(all bool) (*types.Container, error) {
	return getDockerContainer("lbrynet", all)
}

func getDockerContainer(name string, all bool) (*types.Container, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}
	filters := filters.NewArgs()
	filters.Add("name", name)
	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{All: all, Filters: filters})
	if err != nil {
		panic(err)
	}
	if len(containers) == 0 {
		return nil, nil
	}
	if len(containers) > 1 {
		return nil, errors.Err("more than one %s container found", name)
	}

	return &containers[0], nil
}

func IsUsingDocker() bool {
	useDocker, err := strconv.ParseBool(os.Getenv("LBRYNET_USE_DOCKER"))
	if err != nil {
		return false
	}
	return useDocker
}

func IsRegTest() bool {
	usesRegtest, err := strconv.ParseBool(os.Getenv("REGTEST"))
	if err != nil {
		return false
	}
	return usesRegtest
}

func GetLbrycrdClient(lbrycrdString string) (*lbrycrd.Client, error) {
	chainName := os.Getenv("CHAINNAME")
	chainParams, ok := lbrycrd.ChainParamsMap[chainName]
	if !ok {
		chainParams = lbrycrd.MainNetParams
	}
	var lbrycrdd *lbrycrd.Client
	var err error
	if lbrycrdString == "" {
		lbrycrdd, err = lbrycrd.NewWithDefaultURL(&chainParams)
		if err != nil {
			return nil, err
		}
	} else {
		lbrycrdd, err = lbrycrd.New(lbrycrdString, &chainParams)
		if err != nil {
			return nil, err
		}
	}

	return lbrycrdd, nil
}

func ShouldCleanOnStartup() bool {
	shouldClean, err := strconv.ParseBool(os.Getenv("CLEAN_ON_STARTUP"))
	if err != nil {
		return false
	}
	return shouldClean
}

func IsLbrynetRunning() (bool, error) {
	if IsUsingDocker() {
		container, err := GetLBRYNetContainer(ONLINE)
		if err != nil {
			return false, err
		}
		return container != nil, nil
	}

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

func CleanForStartup() error {
	if !IsRegTest() {
		return errors.Err("never cleanup wallet outside of regtest and with caution. this should only be done in local testing and requires regtest to be on")
	}

	running, err := IsLbrynetRunning()
	if err != nil {
		return err
	}
	if running {
		err := StopDaemon()
		if err != nil {
			return err
		}
	}

	err = CleanupLbrynet()
	if err != nil {
		return errors.Err(err)
	}

	lbrycrd, err := GetLbrycrdClient(os.Getenv("LBRYCRD_STRING"))
	if err != nil {
		return errors.Prefix("error getting lbrycrd client: ", err)
	}
	height, err := lbrycrd.GetBlockCount()
	if err != nil {
		return errors.Err(err)
	}
	const minBlocksForUTXO = 200
	if height < minBlocksForUTXO {
		//Start reg test with some credits
		txs, err := lbrycrd.Generate(uint32(minBlocksForUTXO) - uint32(height))
		if err != nil {
			return errors.Err(err)
		}
		log.Debugf("REGTEST: Generated %d transactions to get some LBC!", len(txs))
	}

	defaultWalletDir := GetDefaultWalletPath()
	_, err = os.Stat(defaultWalletDir)
	if os.IsNotExist(err) {
		return nil
	}
	return errors.Err(os.Remove(defaultWalletDir))
}

func CleanupLbrynet() error {
	//make sure lbrynet is off
	running, err := IsLbrynetRunning()
	if err != nil {
		return err
	}
	if running {
		return errors.Prefix("cannot cleanup lbrynet as the daemon is running", err)
	}
	lbrynetDir := GetLBRYNetDir()
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
	blobsDir := GetBlobsDir()
	err = os.RemoveAll(blobsDir)
	if err != nil {
		return errors.Err(err)
	}
	err = os.Mkdir(blobsDir, 0777)
	if err != nil {
		return errors.Err(err)
	}

	lbryumDir := GetLbryumDir()
	ledger := "lbc_mainnet"
	if IsRegTest() {
		ledger = "lbc_regtest"
	}
	lbryumDir = lbryumDir + ledger

	files, err = filepath.Glob(lbryumDir + "/blockchain.db*")
	if err != nil {
		return errors.Err(err)
	}
	for _, f := range files {
		err = os.Remove(f)
		if err != nil {
			return errors.Err(err)
		}
	}
	return nil
}

var metadataDirInitialized = false

func GetVideoMetadataDir() string {
	dir := "./videos_metadata"
	if !metadataDirInitialized {
		metadataDirInitialized = true
		_ = os.MkdirAll(dir, 0755)
	}
	return dir
}

func CleanupMetadata() error {
	dir := GetVideoMetadataDir()
	err := os.RemoveAll(dir)
	if err != nil {
		return errors.Err(err)
	}
	metadataDirInitialized = false
	return nil
}

func SleepUntilQuotaReset() {
	PST, _ := time.LoadLocation("America/Los_Angeles")
	t := time.Now().In(PST)
	n := time.Date(t.Year(), t.Month(), t.Day(), 24, 2, 0, 0, PST)
	d := n.Sub(t)
	if d < 0 {
		n = n.Add(24 * time.Hour)
		d = n.Sub(t)
	}
	log.Infof("gotta sleep %s until the quota resets", d.String())
	time.Sleep(d)
}

func StartDaemon() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("startDaemon").Add(time.Since(start))
	}(start)
	if IsUsingDocker() {
		return startDaemonViaDocker()
	}
	return startDaemonViaSystemd()
}

func StopDaemon() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("stopDaemon").Add(time.Since(start))
	}(start)
	if IsUsingDocker() {
		return stopDaemonViaDocker()
	}
	return stopDaemonViaSystemd()
}

func startDaemonViaDocker() error {
	container, err := GetLBRYNetContainer(true)
	if err != nil {
		return err
	}

	cli, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}

	err = cli.ContainerStart(context.Background(), container.ID, types.ContainerStartOptions{})
	if err != nil {
		return errors.Err(err)
	}

	return nil
}

func stopDaemonViaDocker() error {
	container, err := GetLBRYNetContainer(ONLINE)
	if err != nil {
		return err
	}

	cli, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}

	err = cli.ContainerStop(context.Background(), container.ID, nil)
	if err != nil {
		return errors.Err(err)
	}

	return nil
}

func startDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "start", "lbrynet.service").Run()
	if err != nil {
		return errors.Err(err)
	}
	return nil
}

func stopDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "stop", "lbrynet.service").Run()
	if err != nil {
		return errors.Err(err)
	}
	return nil
}

func GetDefaultWalletPath() string {
	defaultWalletDir := os.Getenv("HOME") + "/.lbryum/wallets/default_wallet"
	if IsRegTest() {
		defaultWalletDir = os.Getenv("HOME") + "/.lbryum_regtest/wallets/default_wallet"
	}

	walletPath := os.Getenv("LBRYUM_DIR")
	if walletPath != "" {
		defaultWalletDir = walletPath + "/wallets/default_wallet"
	}
	return defaultWalletDir
}
func GetBlockchainDBPath() string {
	lbryumDir := os.Getenv("LBRYUM_DIR")
	if lbryumDir == "" {
		if IsRegTest() {
			lbryumDir = os.Getenv("HOME") + "/.lbryum_regtest"
		} else {
			lbryumDir = os.Getenv("HOME") + "/.lbryum"
		}
	}
	defaultDB := lbryumDir + "/lbc_mainnet/blockchain.db"
	if IsRegTest() {
		defaultDB = lbryumDir + "/lbc_regtest/blockchain.db"
	}
	return defaultDB
}
func GetBlockchainDirectoryName() string {
	ledger := "lbc_mainnet"
	if IsRegTest() {
		ledger = "lbc_regtest"
	}
	return ledger
}

func DirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}
