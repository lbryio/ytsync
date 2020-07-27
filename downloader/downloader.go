package downloader

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"os/exec"
	"strings"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/sirupsen/logrus"
)

func GetPlaylistVideoIDs(channelName string, maxVideos int) ([]string, error) {
	args := []string{"--skip-download", "https://www.youtube.com/channel/" + channelName, "--get-id", "--flat-playlist"}
	ids, err := run(args, true, true)
	if err != nil {
		return nil, errors.Err(err)
	}
	videoIDs := make([]string, maxVideos)
	for i, v := range ids {
		if i >= maxVideos {
			break
		}
		videoIDs[i] = v
	}
	return videoIDs, nil
}

func GetVideoInformation(videoID string) (*ytdl.YtdlVideo, error) {
	args := []string{"--skip-download", "--print-json", "https://www.youtube.com/watch?v=" + videoID}
	results, err := run(args, false, true)
	if err != nil {
		return nil, errors.Err(err)
	}
	var video *ytdl.YtdlVideo
	err = json.Unmarshal([]byte(results[0]), &video)
	if err != nil {
		return nil, errors.Err(err)
	}
	return video, nil
}

func run(args []string, withStdErr, withStdOut bool) ([]string, error) {
	cmd := exec.Command("youtube-dl", args...)
	logrus.Printf("Running command youtube-dl %s", strings.Join(args, " "))

	var stderr io.ReadCloser
	var errorLog []byte
	if withStdErr {
		var err error
		stderr, err = cmd.StderrPipe()
		if err != nil {
			return nil, errors.Err(err)
		}
		errorLog, err = ioutil.ReadAll(stderr)
		if err != nil {
			return nil, errors.Err(err)
		}
	}

	var stdout io.ReadCloser
	var outLog []byte
	if withStdOut {
		var err error
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return nil, errors.Err(err)
		}

		if err := cmd.Start(); err != nil {
			return nil, errors.Err(err)
		}
		outLog, err = ioutil.ReadAll(stdout)
		if err != nil {
			return nil, errors.Err(err)
		}
	}
	err := cmd.Wait()
	if len(errorLog) > 0 {
		return nil, errors.Err(err)
	}
	if len(errorLog) > 0 {
		return nil, errors.Err(string(errorLog))
	}
	return strings.Split(strings.Replace(string(outLog), "\r\n", "\n", -1), "\n"), nil
}
