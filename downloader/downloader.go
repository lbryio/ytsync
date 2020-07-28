package downloader

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/util"

	"github.com/sirupsen/logrus"
)

func GetPlaylistVideoIDs(channelName string, maxVideos int) ([]string, error) {
	args := []string{"--skip-download", "https://www.youtube.com/channel/" + channelName, "--get-id", "--flat-playlist"}
	ids, err := run(args, false, true)
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

	// now get an accurate time
	tries := 0
GetTime:
	tries++
	t, err := getUploadTime(videoID)
	if err != nil {
		if errors.Is(err, errNotScraped) && tries <= 3 {
			triggerScrape(videoID)
			time.Sleep(2 * time.Second) // let them scrape it
			goto GetTime
		}
		//return video, errors.Err(err) // just swallow this error and do fallback below
	}

	if t != "" {
		parsed, err := time.Parse("2006-01-02, 15:04:05 (MST)", t) // this will probably be UTC, but Go's timezone parsing is fucked up. it ignores the timezone in the date
		if err != nil {
			return nil, errors.Err(err)
		}
		video.UploadDateForReal = parsed
	} else {
		_ = util.SendToSlack(":warning: Could not get accurate time for %s. Falling back to estimated time.", videoID)
		// fall back to UploadDate from youtube-dl
		video.UploadDateForReal, err = time.Parse("20060102", video.UploadDate)
		if err != nil {
			return nil, err
		}
	}

	return video, nil
}

var errNotScraped = errors.Base("not yet scraped by caa.iti.gr")

func triggerScrape(videoID string) error {
	res, err := http.Get("https://caa.iti.gr/verify_videoV3?twtimeline=0&url=https://www.youtube.com/watch?v=" + videoID)
	if err != nil {
		return errors.Err(err)
	}
	defer res.Body.Close()

	return nil
	//https://caa.iti.gr/caa/api/v4/videos/reports/h-tuxHS5lSM
}

func getUploadTime(videoID string) (string, error) {
	res, err := http.Get("https://caa.iti.gr/get_verificationV3?url=https://www.youtube.com/watch?v=" + videoID)
	if err != nil {
		return "", errors.Err(err)
	}
	defer res.Body.Close()

	var uploadTime struct {
		Time    string `json:"video_upload_time"`
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	err = json.NewDecoder(res.Body).Decode(&uploadTime)
	if err != nil {
		return "", errors.Err(err)
	}

	if uploadTime.Status == "ERROR1" {
		return "", errNotScraped
	}

	return uploadTime.Time, nil
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
