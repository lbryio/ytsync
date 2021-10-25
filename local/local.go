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
	"regexp"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/abadojack/whatlanggo"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"
	"github.com/lbryio/ytsync/v5/namer"
	"github.com/lbryio/ytsync/v5/tags_manager"

	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/util"
)

type SyncContext struct {
	TempDir         string
	LbrynetAddr     string
	ChannelID       string
	PublishBid      float64
}

func (c *SyncContext) Validate() error {
	if c.TempDir == "" {
		return errors.New("No TempDir provided")
	}
	if c.LbrynetAddr == "" {
		return errors.New("No Lbrynet address provided")
	}
	if c.ChannelID == "" {
		return errors.New("No channel ID provided")
	}
	if c.PublishBid <= 0.0 {
		return errors.New("Publish bid is not greater than zero")
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
	cmd.Flags().Float64Var(&syncContext.PublishBid, "publish-bid", 0.01, "Bid amount for the stream claim")
	cmd.Flags().StringVar(&syncContext.LbrynetAddr, "lbrynet-address", getEnvDefault("LBRYNET_ADDRESS", ""), "JSONRPC address of the local LBRYNet daemon")
	cmd.Flags().StringVar(&syncContext.ChannelID, "channel-id", "", "LBRY channel ID to publish to")
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

	if !status.IsRunning {
		log.Error("SDK is not running")
		return
	}

	// Should check to see if the SDK owns the channel

	// Should check to see if wallet is unlocked
	// but jsonrpc.Client doesn't have WalletStatus method
	// so skip for now

	// Should check to see if streams are configured to be reflected and warn if not
	// but jsonrpc.Client doesn't have SettingsGet method to see if streams are reflected
	// so use File.UploadingToReflector as a proxy for now


	videoBasePath := path.Join(syncContext.TempDir, videoID)

	videoMetadata, videoMetadataPath, err := getVideoMetadata(videoBasePath, videoID)
	if err != nil {
		log.Errorf("Error getting video metadata: %v", err)
		return
	}

	err = downloadVideo(videoBasePath, videoMetadataPath)
	if err != nil {
		log.Errorf("Error downloading video: %v", err)
		return
	}

	tags, err := tags_manager.SanitizeTags(videoMetadata.Tags, syncContext.ChannelID)
	if err != nil {
		log.Errorf("Error sanitizing tags: %v", err)
		return
	}

	urlsRegex := regexp.MustCompile(`(?m) ?(f|ht)(tp)(s?)(://)(.*)[.|/](.*)`)
	descriptionSample := urlsRegex.ReplaceAllString(videoMetadata.Description, "")
	info := whatlanggo.Detect(descriptionSample)
	info2 := whatlanggo.Detect(videoMetadata.Title)
	var languages []string = nil
	if info.IsReliable() && info.Lang.Iso6391() != "" {
		language := info.Lang.Iso6391()
		languages = []string{language}
	} else if info2.IsReliable() && info2.Lang.Iso6391() != "" {
		language := info2.Lang.Iso6391()
		languages = []string{language}
	}
	// Thumbnail and ReleaseTime need to be properly determined
	streamCreateOptions := jsonrpc.StreamCreateOptions {
		ClaimCreateOptions: jsonrpc.ClaimCreateOptions {
			Title:        &videoMetadata.Title,
			Description:  util.PtrToString(getAbbrevDescription(videoMetadata)),
			Languages:    languages,
			//ThumbnailURL: &v.thumbnailURL,
			Tags:         tags,
		},
		ReleaseTime: util.PtrToInt64(time.Now().Unix()),
		ChannelID:   &syncContext.ChannelID,
		License:     util.PtrToString("Copyrighted (contact publisher)"),
	}

	videoPath, err := getVideoDownloadedPath(syncContext.TempDir, videoID)
	if err != nil {
		log.Errorf("Error determining downloaded video path: %v", err)
	}

	fmt.Println("%s", *streamCreateOptions.ClaimCreateOptions.Title)
	fmt.Println("%s", *streamCreateOptions.ClaimCreateOptions.Description)
	fmt.Println("%v", streamCreateOptions.ClaimCreateOptions.Languages)
	fmt.Println("%v", streamCreateOptions.ClaimCreateOptions.Tags)

	claimName := namer.NewNamer().GetNextName(videoMetadata.Title)
	log.Infof("Publishing stream as %s", claimName)

	txSummary, err := lbrynet.StreamCreate(claimName, videoPath, syncContext.PublishBid, streamCreateOptions)
	if err != nil {
		log.Errorf("Error creating stream: %v", err)
		return
	}

	for {
		fileListResponse, fileIndex, err := findFileByTxid(lbrynet, txSummary.Txid)
		if err != nil {
			log.Errorf("Error finding file by txid: %v", err)
			return
		}
		if fileListResponse == nil {
			log.Errorf("Could not find file in list with correct txid")
			return
		}

		fileStatus := fileListResponse.Items[fileIndex]
		if fileStatus.IsFullyReflected {
			log.Info("Stream is fully reflected")
			break
		}
		if !fileStatus.UploadingToReflector {
			log.Warn("Stream is not being uploaded to a reflector. Check your lbrynet settings if this is a mistake.")
			break
		}
		log.Infof("Stream reflector progress: %d%%", fileStatus.ReflectorProgress)
		time.Sleep(5 * time.Second)
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
	metadataBytes, err := os.ReadFile(path)
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

func getVideoDownloadedPath(videoDir, videoID string) (string, error) {
	files, err := ioutil.ReadDir(videoDir)
	if err != nil {
		return "", err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if path.Ext(f.Name()) == ".mp4" && strings.Contains(f.Name(), videoID) {
			return path.Join(videoDir, f.Name()), nil
		}
	}
	return "", errors.New("could not find any downloaded videos")

}

func getAbbrevDescription(v *ytdl.YtdlVideo) string {
	maxLength := 2800
	description := strings.TrimSpace(v.Description)
	additionalDescription := "\nhttps://www.youtube.com/watch?v=" + v.ID
	if len(description) > maxLength {
		description = description[:maxLength]
	}
	return description + "\n..." + additionalDescription
}

// if jsonrpc.Client.FileList is extended to match the actual jsonrpc schema, this can be removed
func findFileByTxid(client *jsonrpc.Client, txid string) (*jsonrpc.FileListResponse, int, error) {
	response, err := client.FileList(0, 20)
	for {
		if err != nil {
			log.Errorf("Error getting file list page: %v", err)
			return nil, 0, err
		}
		index := sort.Search(len(response.Items), func (i int) bool { return response.Items[i].Txid == txid })
		if index < len(response.Items) {
			return response, index, nil
		}
		if response.Page >= response.TotalPages {
			return nil, 0, nil
		}
		response, err = client.FileList(response.Page + 1, 20)
	}
}
