package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"time"
	"os"
	"os/exec"
	"path"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"

	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
)

type SyncContext struct {
	TempDir         string
	LbrynetAddr     string
}

func (c *SyncContext) Validate() error {
	if c.TempDir == "" {
		return errors.New("No TempDir provided")
	}
	if c.LbrynetAddr == "" {
		return errors.New("No Lbrynet address provided")
	}
	return nil
}

var syncContext SyncContext

func AddCommand(rootCmd *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "run a personal ytsync",
		Run:   localCmd,
		Args:  cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&syncContext.TempDir, "temp-dir", getEnvDefault("TEMP_DIR", ""), "directory to use for temporary files")
	cmd.Flags().StringVar(&syncContext.LbrynetAddr, "lbrynet-address", getEnvDefault("LBRYNET_ADDRESS", ""), "JSONRPC address of the local LBRYNet daemon")
	rootCmd.AddCommand(cmd)
}

func getEnvDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

func localCmd(cmd *cobra.Command, args []string) {
	err := syncContext.Validate()
	if err != nil {
		log.Error(err)
		return
	}
	fmt.Println(syncContext.LbrynetAddr)

	videoID := args[0]
	fmt.Println(videoID)

	lbrynet := jsonrpc.NewClient(syncContext.LbrynetAddr)
	lbrynet.SetRPCTimeout(5 * time.Minute)

	status, err := lbrynet.Status()
	if err != nil {
		log.Error(err)
		return
	}

	fmt.Println(status.IsRunning)
	fmt.Println(status.Wallet.Connected)

	videoBasePath := path.Join(syncContext.TempDir, videoID)

	_, videoMetadataPath, err := getVideoMetadata(videoBasePath, videoID)
	if err != nil {
		log.Errorf("Error getting video metadata: %v", err)
		return
	}

	err = downloadVideo(videoBasePath, videoMetadataPath)
	if err != nil {
		log.Errorf("Error downloading video: %v", err)
	}

	log.Info("Done")
}

func getVideoMetadata(basePath, videoID string) (*ytdl.YtdlVideo, string, error) {
	metadataPath := basePath + ".info.json"

	_, err := os.Stat(metadataPath)
	if err != nil && !os.IsNotExist(err) {
		log.Errorf("Error determining if video metadata already exists: %v", err)
		return nil, "", err
	} else if err == nil {
		log.Debugf("Video metadata file %s already exists. Attempting to load existing file.", metadataPath)
		videoMetadata, err := loadVideoMetadata(metadataPath)
		if err != nil {
			log.Debugf("Error loading pre-existing video metadata: %v. Deleting file and attempting re-download.", err)
		} else {
			return videoMetadata, metadataPath, nil
		}
	}

	if err := downloadVideoMetadata(basePath, videoID); err != nil {
		return nil, "", err
	}

	videoMetadata, err := loadVideoMetadata(metadataPath)
	return videoMetadata, metadataPath, err
}

func loadVideoMetadata(path string) (*ytdl.YtdlVideo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	metadataBytes, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	var videoMetadata *ytdl.YtdlVideo
	err = json.Unmarshal(metadataBytes, &videoMetadata)
	if err != nil {
		return nil, err
	}

	return videoMetadata, nil
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
