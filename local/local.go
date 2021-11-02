package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/abadojack/whatlanggo"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"
	"github.com/lbryio/ytsync/v5/namer"
	"github.com/lbryio/ytsync/v5/tags_manager"
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

	log.Debugf("Running sync for YouTube video ID %s", videoID)

	var publisher VideoPublisher
	publisher, err = NewLocalSDKPublisher(syncContext.LbrynetAddr, syncContext.ChannelID, syncContext.PublishBid)
	if err != nil {
		log.Errorf("Error setting up publisher: %v", err)
		return
	}

	var videoSource VideoSource
	videoSource, err = NewYtdlVideoSource(syncContext.TempDir)
	if err != nil {
		log.Errorf("Error setting up video source: %v", err)
		return
	}

	sourceVideo, err := videoSource.GetVideo(videoID)
	if err != nil {
		log.Errorf("Error getting source video: %v", err)
		return
	}

	processedVideo, err := processVideoForPublishing(*sourceVideo, syncContext.ChannelID)
	if err != nil {
		log.Errorf("Error processing source video for publishing: %v", err)
		return
	}

	done, err := publisher.Publish(*processedVideo)
	if err != nil {
		log.Errorf("Error publishing video: %v", err)
		return
	}

	err = <-done
	if err != nil {
		log.Errorf("Error while wating for stream to reflect: %v", err)
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

func getAbbrevDescription(v SourceVideo) string {
	if v.Description == nil {
		return v.SourceURL
	}

	maxLength := 2800
	description := strings.TrimSpace(*v.Description)
	additionalDescription := "\n" + v.SourceURL
	if len(description) > maxLength {
		description = description[:maxLength]
	}
	return description + "\n..." + additionalDescription
}

type SourceVideo struct {
	ID string
	Title *string
	Description *string
	SourceURL string
	Languages []string
	Tags []string
	ReleaseTime *int64
	ThumbnailURL *string
	FullLocalPath string
}

type PublishableVideo struct {
	ID string
	ClaimName string
	Title string
	Description string
	SourceURL string
	Languages []string
	Tags []string
	ReleaseTime int64
	ThumbnailURL string
	FullLocalPath string
}

func processVideoForPublishing(source SourceVideo, channelID string) (*PublishableVideo, error) {
	tags, err := tags_manager.SanitizeTags(source.Tags, channelID)
	if err != nil {
		log.Errorf("Error sanitizing tags: %v", err)
		return nil, err
	}

	descriptionSample := ""
	if source.Description != nil {
		urlsRegex := regexp.MustCompile(`(?m) ?(f|ht)(tp)(s?)(://)(.*)[.|/](.*)`)
		descriptionSample = urlsRegex.ReplaceAllString(*source.Description, "")
	}
	info := whatlanggo.Detect(descriptionSample)

	title := ""
	if source.Title != nil {
		title = *source.Title
	}
	info2 := whatlanggo.Detect(title)
	var languages []string = nil
	if info.IsReliable() && info.Lang.Iso6391() != "" {
		language := info.Lang.Iso6391()
		languages = []string{language}
	} else if info2.IsReliable() && info2.Lang.Iso6391() != "" {
		language := info2.Lang.Iso6391()
		languages = []string{language}
	}

	claimName := namer.NewNamer().GetNextName(title)

	thumbnailURL := ""
	if source.ThumbnailURL != nil {
		thumbnailURL = *source.ThumbnailURL
	}

	processed := PublishableVideo {
		ClaimName: claimName,
		Title: title,
		Description:  getAbbrevDescription(source),
		Languages: languages,
		Tags: tags,
		ReleaseTime: *source.ReleaseTime,
		ThumbnailURL: thumbnailURL,
		FullLocalPath: source.FullLocalPath,
	}

	log.Debugf("Video prepared for publication: %v", processed)

	return &processed, nil
}

type VideoSource interface {
	GetVideo(id string) (*SourceVideo, error)
}

type VideoPublisher interface {
	Publish(video PublishableVideo) (chan error, error)
}
