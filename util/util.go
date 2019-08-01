package util

import (
	"context"
	"fmt"
	"os"
	"os/user"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/lbrycrd"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
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
	for _, container := range containers {
		fmt.Printf("%s %s\n", container.ID[:10], container.Image)
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
	return os.Getenv("LBRYNET_USE_DOCKER") == "true"
}

func IsRegTest() bool {
	return os.Getenv("REGTEST") == "true"
}

func GetLbrycrdClient(lbrycrdString string) (*lbrycrd.Client, error) {
	var lbrycrdd *lbrycrd.Client
	var err error
	if lbrycrdString == "" {
		lbrycrdd, err = lbrycrd.NewWithDefaultURL()
		if err != nil {
			return nil, err
		}
	} else {
		lbrycrdd, err = lbrycrd.New(lbrycrdString)
		if err != nil {
			return nil, err
		}
	}

	return lbrycrdd, nil
}
