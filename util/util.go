package util

import (
	"context"
	"fmt"
	"os"
	"os/user"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/lbryio/lbry.go/extras/errors"

	"github.com/prometheus/common/log"
)

func GetBlobsDir() string {
	blobsDir := os.Getenv("BLOBS_DIRECTORY")
	if blobsDir == "" {
		usr, err := user.Current()
		if err != nil {
			log.Errorln(err.Error())
			return ""
		}
		blobsDir = usr.HomeDir + "/.lbrynet/blobfiles/"
	}

	return blobsDir
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

func GetLBRYNetContainer(all bool) (*types.Container, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}
	filters := filters.NewArgs()
	filters.Add("name", "lbrynet")
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
		return nil, errors.Err("more than one lbrynet container found")
	}

	return &containers[0], nil
}

func IsUsingDocker() bool {
	return os.Getenv("LBRYNET_USE_DOCKER") == "true"
}
