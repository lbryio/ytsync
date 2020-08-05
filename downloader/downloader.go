package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/lbryio/ytsync/v5/downloader/ytdl"
	"github.com/lbryio/ytsync/v5/ip_manager"
	"github.com/lbryio/ytsync/v5/sdk"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/stop"
	"github.com/lbryio/lbry.go/v2/extras/util"

	"github.com/sirupsen/logrus"
)

func GetPlaylistVideoIDs(channelName string, maxVideos int, stopChan stop.Chan, pool *ip_manager.IPPool) ([]string, error) {
	args := []string{"--skip-download", "https://www.youtube.com/channel/" + channelName, "--get-id", "--flat-playlist"}
	ids, err := run(channelName, args, true, true, stopChan, pool)
	if err != nil {
		return nil, errors.Err(err)
	}
	videoIDs := make([]string, maxVideos)
	for i, v := range ids {
		logrus.Debugf("%d - video id %s", i, v)
		if i >= maxVideos {
			break
		}
		videoIDs[i] = v
	}
	return videoIDs, nil
}

const releaseTimeFormat = "2006-01-02, 15:04:05 (MST)"

func GetVideoInformation(config *sdk.APIConfig, videoID string, stopChan stop.Chan, ip *net.TCPAddr, pool *ip_manager.IPPool) (*ytdl.YtdlVideo, error) {
	args := []string{"--skip-download", "--print-json", "https://www.youtube.com/watch?v=" + videoID}
	results, err := run(videoID, args, true, true, stopChan, pool)
	if err != nil {
		return nil, errors.Err(err)
	}
	var video *ytdl.YtdlVideo
	err = json.Unmarshal([]byte(results[0]), &video)
	if err != nil {
		return nil, errors.Err(err)
	}

	// now get an accurate time
	const maxTries = 5
	tries := 0
GetTime:
	tries++
	t, err := getUploadTime(config, videoID, ip, video.UploadDate)
	if err != nil {
		//slack(":warning: Upload time error: %v", err)
		if tries <= maxTries && (errors.Is(err, errNotScraped) || errors.Is(err, errUploadTimeEmpty) || errors.Is(err, errStatusParse) || errors.Is(err, errConnectionIssue)) {
			err := triggerScrape(videoID, ip)
			if err == nil {
				time.Sleep(2 * time.Second) // let them scrape it
				goto GetTime
			} else {
				//slack("triggering scrape returned error: %v", err)
			}
		} else if !errors.Is(err, errNotScraped) && !errors.Is(err, errUploadTimeEmpty) {
			//slack(":warning: Error while trying to get accurate upload time for %s: %v", videoID, err)
			return nil, errors.Err(err)
		}
		// do fallback below
	}
	//slack("After all that, upload time for %s is %s", videoID, t)

	if t != "" {
		parsed, err := time.Parse("2006-01-02, 15:04:05 (MST)", t) // this will probably be UTC, but Go's timezone parsing is fucked up. it ignores the timezone in the date
		if err != nil {
			return nil, errors.Err(err)
		}
		slack(":exclamation: Got an accurate time for %s", videoID)
		video.UploadDateForReal = parsed
	} else {
		//slack(":warning: Could not get accurate time for %s. Falling back to time from upload ytdl: %s.", videoID, video.UploadDate)
		// fall back to UploadDate from youtube-dl
		video.UploadDateForReal, err = time.Parse("20060102", video.UploadDate)
		if err != nil {
			return nil, err
		}
	}

	return video, nil
}

var errNotScraped = errors.Base("not yet scraped by caa.iti.gr")
var errUploadTimeEmpty = errors.Base("upload time is empty")
var errStatusParse = errors.Base("could not parse status, got number, need string")
var errConnectionIssue = errors.Base("there was a connection issue with the api")

func slack(format string, a ...interface{}) {
	fmt.Printf(format+"\n", a...)
	util.SendToSlack(format, a...)
}

func triggerScrape(videoID string, ip *net.TCPAddr) error {
	//slack("Triggering scrape for %s", videoID)
	u, err := url.Parse("https://caa.iti.gr/verify_videoV3")
	q := u.Query()
	q.Set("twtimeline", "0")
	q.Set("url", "https://www.youtube.com/watch?v="+videoID)
	u.RawQuery = q.Encode()
	//slack("GET %s", u.String())

	client := getClient(ip)
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.Err(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 6.2; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/60.0.3112.90 Safari/537.36")

	res, err := client.Do(req)
	if err != nil {
		return errors.Err(err)
	}
	defer res.Body.Close()

	var response struct {
		Message  string `json:"message"`
		Status   string `json:"status"`
		VideoURL string `json:"video_url"`
	}
	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		if strings.Contains(err.Error(), "cannot unmarshal number") {
			return errors.Err(errStatusParse)
		}
		if strings.Contains(err.Error(), "no route to host") {
			return errors.Err(errConnectionIssue)
		}
		return errors.Err(err)
	}

	switch response.Status {
	case "removed_video":
		return errors.Err("video previously removed from service")
	case "no_video":
		return errors.Err("they say 'video cannot be found'. wtf?")
	default:
		spew.Dump(response)
	}

	return nil
	//https://caa.iti.gr/caa/api/v4/videos/reports/h-tuxHS5lSM
}

func getUploadTime(config *sdk.APIConfig, videoID string, ip *net.TCPAddr, uploadDate string) (string, error) {
	//slack("Getting upload time for %s", videoID)
	release, err := config.GetReleasedDate(videoID)
	if err != nil {
		logrus.Error(err)
	}
	if release != nil {
		const sqlTimeFormat = "2006-01-02 15:04:05"
		sqlTime, err := time.ParseInLocation(sqlTimeFormat, release.ReleaseTime, time.UTC)
		if err == nil {
			return sqlTime.Format(releaseTimeFormat), nil
		}
	}
	ytdlUploadDate, err := time.Parse("20060102", uploadDate)
	if err != nil {
		logrus.Error(err)
	}
	if time.Now().AddDate(0, 0, -5).After(ytdlUploadDate) {
		return ytdlUploadDate.Format(releaseTimeFormat), nil
	}
	client := getClient(ip)
	req, err := http.NewRequest(http.MethodGet, "https://caa.iti.gr/get_verificationV3?url=https://www.youtube.com/watch?v="+videoID, nil)
	if err != nil {
		return "", errors.Err(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 6.2; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/60.0.3112.90 Safari/537.36")

	res, err := client.Do(req)
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

	if uploadTime.Status == "" && strings.HasPrefix(uploadTime.Message, "CANNOT_RETRIEVE_REPORT_FOR_VIDEO_") {
		return "", errors.Err("cannot retrieve report for video")
	}

	if uploadTime.Time == "" {
		return "", errUploadTimeEmpty
	}

	return uploadTime.Time, nil
}

func getClient(ip *net.TCPAddr) *http.Client {
	if ip == nil {
		return http.DefaultClient
	}

	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				LocalAddr: ip,
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func run(use string, args []string, withStdErr, withStdOut bool, stopChan stop.Chan, pool *ip_manager.IPPool) ([]string, error) {
	var maxtries = 10
	var attemps int
	var useragent []string
	for {
		sourceAddress, err := getIPFromPool(use, stopChan, pool)
		if err != nil {
			return nil, err
		}
		defer pool.ReleaseIP(sourceAddress)
		argsForCommand := append(args, "--source-address", sourceAddress)
		argsForCommand = append(argsForCommand, useragent...)
		cmd := exec.Command("youtube-dl", argsForCommand...)
		logrus.Printf("Running command youtube-dl %s", strings.Join(argsForCommand, " "))

		var stderr io.ReadCloser
		var errorLog []byte
		if withStdErr {
			var err error
			stderr, err = cmd.StderrPipe()
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
			if withStdErr {
				errorLog, err = ioutil.ReadAll(stderr)
				if err != nil {
					return nil, errors.Err(err)
				}
			}
		}

		done := make(chan error, 1)
		go func() {
			attemps++
			done <- cmd.Wait()
		}()
		select {
		case <-stopChan:
			if err := cmd.Process.Kill(); err != nil {
				return nil, errors.Prefix("failed to kill command after stopper cancellation", err)
			}
			return nil, errors.Err("canceled by stopper")
		case err := <-done:
			if err != nil {
				if strings.Contains(err.Error(), "exit status 1") {
					if strings.Contains(string(errorLog), "HTTP Error 429") || strings.Contains(string(errorLog), "returned non-zero exit status 8") {
						pool.SetThrottled(sourceAddress)
						logrus.Debugf("known throttling error...try again (%d)", attemps)
						continue
					}
					if strings.Contains(string(errorLog), "YouTube said: Unable to extract video data") {
						useragent = []string{"--user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.88 Safari/537.36"}
						if attemps == 1 {
							useragent = []string{"--user-agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"}
						}
						logrus.Debugf("known extraction issue, maybe user agent specification will work...try again (%d)", attemps)
						continue
					}
					if attemps > maxtries {
						logrus.Debug("too many tries returning failure")
						break
					}
				}
				logrus.Debugf("Unkown error, returning failure: %s", err.Error())
				return nil, errors.Prefix("youtube-dl "+strings.Join(argsForCommand, " ")+" ["+string(errorLog)+"] ", err)
			}
			return strings.Split(strings.Replace(string(outLog), "\r\n", "\n", -1), "\n"), nil
		}

		if len(errorLog) > 0 {
			return nil, errors.Err(string(errorLog))
		}
	}
}

func getIPFromPool(use string, stopChan stop.Chan, pool *ip_manager.IPPool) (sourceAddress string, err error) {
	for {
		sourceAddress, err = pool.GetIP(use)
		if err != nil {
			if errors.Is(err, ip_manager.ErrAllThrottled) {
				select {
				case <-stopChan:
					return "", errors.Err("interrupted by user")

				default:
					time.Sleep(ip_manager.IPCooldownPeriod)
					continue
				}
			} else {
				return "", err
			}
		}
		break
	}
	return
}
