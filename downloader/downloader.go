package downloader

import (
	"io/ioutil"
	"os/exec"
	"strings"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/sirupsen/logrus"
)

func GetPlaylistVideoIDs(channelName string) ([]string, error) {
	args := []string{"--skip-download", "https://www.youtube.com/channel/" + channelName, "--get-id", "--flat-playlist"}
	return run(args)
}

func run(args []string) ([]string, error) {
	cmd := exec.Command("youtube-dl", args...)
	logrus.Printf("Running command youtube-dl %s", strings.Join(args, " "))

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, errors.Err(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Err(err)
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.Err(err)
	}

	errorLog, _ := ioutil.ReadAll(stderr)
	outLog, _ := ioutil.ReadAll(stdout)
	err = cmd.Wait()
	if len(errorLog) > 0 {
		return nil, errors.Err(err)
	}
	if len(errorLog) > 0 {
		return nil, errors.Err(string(errorLog))
	}
	return strings.Split(strings.Replace(string(outLog), "\r\n", "\n", -1), "\n"), nil
}
