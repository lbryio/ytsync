package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"
)

type Ytdl struct {
	DownloadDir string
}

func NewYtdl(downloadDir string) (*Ytdl, error) {
	// TODO validate download dir

	y := Ytdl {
		DownloadDir: downloadDir,
	}

	return &y, nil
}

func (y *Ytdl) GetVideoMetadata(videoID string) (*ytdl.YtdlVideo, error) {
	metadataPath, err := y.GetVideoMetadataFile(videoID)
	if err != nil {
		return nil, err
	}

	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, err
	}

	var metadata *ytdl.YtdlVideo
	err = json.Unmarshal(metadataBytes, &metadata)
	if err != nil {
		return nil, err
	}

	return metadata, nil
}


func (y *Ytdl) GetVideoMetadataFile(videoID string) (string, error) {
	basePath := path.Join(y.DownloadDir, videoID)
	metadataPath := basePath + ".info.json"

	_, err := os.Stat(metadataPath)
	if err != nil && !os.IsNotExist(err) {
		log.Errorf("Error determining if video metadata already exists: %v", err)
		return "", err
	} else if err != nil {
		log.Debugf("Metadata file for video %s does not exist. Downloading now.", videoID)
		err = downloadVideoMetadata(basePath, videoID)
		if err != nil {
			return "", err
		}
	}

	return metadataPath, nil
}

func (y *Ytdl) GetVideoFile(videoID string) (string, error) {
	videoPath, err := findDownloadedVideo(y.DownloadDir, videoID)
	if err != nil {
		return "", err
	}

	if videoPath != nil {
		return *videoPath, nil
	}

	basePath := path.Join(y.DownloadDir, videoID)
	metadataPath, err := y.GetVideoMetadataFile(videoID)
	if err != nil {
		log.Errorf("Error getting metadata path in preparation for video download: %v", err)
		return "", err
	}
	err = downloadVideo(basePath, metadataPath)
	if err != nil {
		return "", nil
	}

	videoPath, err = findDownloadedVideo(y.DownloadDir, videoID)
	if err != nil {
		log.Errorf("Error from findDownloadedVideo() after already succeeding once: %v", err)
		return "", err
	}
	if videoPath == nil {
		return "", errors.New("Could not find a downloaded video after successful download.")
	}

	return *videoPath, nil
}

func (y *Ytdl) DeleteVideoFiles(videoID string) error {
	files, err := ioutil.ReadDir(y.DownloadDir)
	if err != nil {
		return err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if strings.Contains(f.Name(), videoID) {
			videoPath := path.Join(y.DownloadDir, f.Name())
			err = os.Remove(videoPath)
			if err != nil {
				log.Errorf("Error while deleting file %s: %v", y.DownloadDir, err)
				return err
			}
		}
	}

	return nil
}

func deleteFile(path string) error {
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		log.Errorf("Error determining if file %s exists: %v", path, err)
		return err
	} else if err != nil {
		log.Debugf("File %s does not exist. Skipping deletion.", path)
		return nil
	}

	return os.Remove(path)
}

func findDownloadedVideo(videoDir, videoID string) (*string, error) {
	files, err := ioutil.ReadDir(videoDir)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if path.Ext(f.Name()) == ".mp4" && strings.Contains(f.Name(), videoID) {
			videoPath := path.Join(videoDir, f.Name())
			return &videoPath, nil
		}
	}
	return nil, nil
}

func downloadVideoMetadata(basePath, videoID string) error {
	ytdlArgs := []string{
		"--skip-download",
		"--write-info-json",
		"--force-overwrites",
		fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID),
		"--cookies",
		"cookies.txt",
		"-o",
		basePath,
	}
	ytdlCmd := exec.Command("yt-dlp", ytdlArgs...)
	output, err := runCmd(ytdlCmd)
	log.Debug(output)
	return err
}

func downloadVideo(basePath, metadataPath string) error {
	ytdlArgs := []string{
		"--no-progress",
		"-o",
		basePath,
		"--merge-output-format",
		"mp4",
		"--postprocessor-args",
		"ffmpeg:-movflags faststart",
		"--abort-on-unavailable-fragment",
		"--fragment-retries",
		"1",
		"--cookies",
		"cookies.txt",
		"--extractor-args",
		"youtube:player_client=android",
		"--load-info-json",
		metadataPath,
		"-fbestvideo[ext=mp4][vcodec!*=av01][height<=720]+bestaudio[ext!=webm][format_id!=258][format_id!=251][format_id!=256][format_id!=327]",
	}

	ytdlCmd := exec.Command("yt-dlp", ytdlArgs...)
	output, err := runCmd(ytdlCmd)
	log.Debug(output)
	return err
}

func runCmd(cmd *exec.Cmd) ([]string, error) {
	log.Infof("running cmd: %s", strings.Join(cmd.Args, " "))
	var err error
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	err = cmd.Start()
	if err != nil {
		return nil, err
	}
	outLog, err := ioutil.ReadAll(stdout)
	if err != nil {
		return nil, err
	}
	errorLog, err := ioutil.ReadAll(stderr)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			log.Error(string(errorLog))
			return nil, err
		}
		return strings.Split(strings.Replace(string(outLog), "\r\n", "\n", -1), "\n"), nil
	}
}
