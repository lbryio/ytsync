package local

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/abadojack/whatlanggo"

	"github.com/lbryio/lbry.go/v2/extras/util"
	"github.com/lbryio/ytsync/v5/namer"
	"github.com/lbryio/ytsync/v5/tags_manager"
)

type SyncContext struct {
	DryRun              bool
	KeepCache           bool
	ReflectStreams      bool
	TempDir             string
	SyncDbPath          string
	LbrynetAddr         string
	ChannelID           string
	VideoID             string
	PublishBid          float64
	YouTubeSourceConfig *YouTubeSourceConfig
}

func (c *SyncContext) Validate() error {
	if c.TempDir == "" {
		return errors.New("No TempDir provided")
	}
	if c.SyncDbPath == "" {
		return errors.New("No sync DB path provided")
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

	if c.YouTubeSourceConfig.ChannelID != "" {
		// Validate for YouTube source
		// For now, an API key is required
		if c.YouTubeSourceConfig.APIKey == "" {
			return errors.New("YouTube source was selected, but no YouTube API key was provided.")
		}
	} else {
		return errors.New("No video source provided")
	}
	return nil
}

type YouTubeSourceConfig struct {
	ChannelID string
	APIKey string
}

var syncContext SyncContext

func AddCommand(rootCmd *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "run a personal ytsync",
		Run:   localCmd,
		Args:  cobra.ExactArgs(0),
	}
	cmd.Flags().BoolVar(&syncContext.DryRun, "dry-run", false, "Display information about the stream publishing, but do not publish the stream")
	cmd.Flags().BoolVar(&syncContext.KeepCache, "keep-cache", false, "Don't delete local files after publishing.")
	cmd.Flags().BoolVar(&syncContext.ReflectStreams, "reflect-streams", true, "Require published streams to be reflected.")
	cmd.Flags().StringVar(&syncContext.TempDir, "temp-dir", getEnvDefault("TEMP_DIR", ""), "directory to use for temporary files")
	cmd.Flags().StringVar(&syncContext.SyncDbPath, "sync-db-path", getEnvDefault("SYNC_DB_PATH", ""), "Path to the local sync DB")
	cmd.Flags().Float64Var(&syncContext.PublishBid, "publish-bid", 0.01, "Bid amount for the stream claim")
	cmd.Flags().StringVar(&syncContext.LbrynetAddr, "lbrynet-address", getEnvDefault("LBRYNET_ADDRESS", ""), "JSONRPC address of the local LBRYNet daemon")
	cmd.Flags().StringVar(&syncContext.ChannelID, "channel-id", "", "LBRY channel ID to publish to")
	cmd.Flags().StringVar(&syncContext.VideoID, "video-id", "", "ID of video to sync. This will attempt to sync only this one video.")

	syncContext.YouTubeSourceConfig = &YouTubeSourceConfig{}
	cmd.Flags().StringVar(&syncContext.YouTubeSourceConfig.APIKey, "youtube-api-key", getEnvDefault("YOUTUBE_API_KEY", ""), "YouTube API Key")
	cmd.Flags().StringVar(&syncContext.YouTubeSourceConfig.ChannelID, "youtube-channel", "", "YouTube Channel ID")
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
	videoID := syncContext.VideoID
	if videoID == "" {
		log.Errorf("Only single video mode is supported currently. Please provided a video ID.")
		return
	}

	log.Debugf("Running sync for video ID %s", videoID)

	syncDB, err := NewSyncDb(syncContext.SyncDbPath)
	if err != nil {
		log.Errorf("Error creating sync DB: %v", err)
		return
	}
	defer syncDB.Close()

	isSynced, claimID, err := syncDB.IsVideoPublished("YouTube", videoID)
	if err != nil {
		log.Errorf("Error checking if video is already synced: %v", err)
		return
	}

	if isSynced {
		log.Infof("Video %s is already published as %s.", videoID, claimID)
		return
	}

	var publisher VideoPublisher
	publisher, err = NewLocalSDKPublisher(syncContext.LbrynetAddr, syncContext.ChannelID, syncContext.PublishBid)
	if err != nil {
		log.Errorf("Error setting up publisher: %v", err)
		return
	}

	var videoSource VideoSource
	if syncContext.YouTubeSourceConfig != nil {
		videoSource, err = NewYtdlVideoSource(syncContext.TempDir, syncContext.YouTubeSourceConfig)
		if err != nil {
			log.Errorf("Error setting up video source: %v", err)
			return
		}
	}

	err = syncVideo(syncContext, syncDB, videoSource, publisher, videoID)
	if err != nil {
		log.Errorf("Error syncing %s: %v", videoID, err)
		return
	}
	log.Info("Done")
}

func syncVideo(syncContext SyncContext, syncDB *SyncDb, videoSource VideoSource, publisher VideoPublisher, videoID string) error {
	sourceVideo, err := videoSource.GetVideo(videoID)
	if err != nil {
		log.Errorf("Error getting source video: %v", err)
		return err
	}

	err = syncDB.SaveVideoData(*sourceVideo)
	if err != nil {
		log.Errorf("Error saving video data: %v", err)
		return err
	}

	processedVideo, err := processVideoForPublishing(*sourceVideo, syncContext.ChannelID)
	if err != nil {
		log.Errorf("Error processing source video for publishing: %v", err)
		return err
	}

	if syncContext.DryRun {
		log.Infoln("This is a dry run. Nothing will be published.")
		log.Infof("The local file %s would be published to channel ID %s as %s.", processedVideo.FullLocalPath, syncContext.ChannelID, processedVideo.ClaimName)
		log.Debugf("Object to be published: %v", processedVideo)

	} else {
		claimID, doneReflectingCh, err := publisher.Publish(*processedVideo, syncContext.ReflectStreams)
		if err != nil {
			log.Errorf("Error publishing video: %v", err)
			return err
		}
		err = syncDB.SaveVideoPublication(*processedVideo, claimID)
		if err != nil {
			// Sync DB is corrupted after getting here
			// and will allow double publication.
			log.Errorf("Error saving video publication to sync DB: %v", err)
			return err
		}

		if syncContext.ReflectStreams {
			err = <-doneReflectingCh
			if err != nil {
				log.Errorf("Error while wating for stream to reflect: %v", err)
				return err
			}
		} else {
			log.Debugln("Not waiting for stream to reflect.")
		}
	}

	if !syncContext.KeepCache {
		log.Infof("Deleting local files.")
		err = videoSource.DeleteLocalCache(videoID)
		if err != nil {
			log.Errorf("Error deleting local files for video %s: %v", videoID, err)
			return err
		}
	}

	return nil
}

type SourceVideo struct {
	ID string
	Source string
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
	Source string
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

	thumbnailURL := source.ThumbnailURL
	if thumbnailURL == nil {
		thumbnailURL = util.PtrToString("")
	}

	releaseTime := source.ReleaseTime
	if releaseTime == nil {
		releaseTime = util.PtrToInt64(time.Now().Unix())
	}

	processed := PublishableVideo {
		ID: source.ID,
		Source: source.Source,
		ClaimName: claimName,
		Title: title,
		Description:  getAbbrevDescription(source),
		Languages: languages,
		Tags: tags,
		ReleaseTime: *releaseTime,
		ThumbnailURL: *thumbnailURL,
		FullLocalPath: source.FullLocalPath,
	}

	log.Debugf("Video prepared for publication: %v", processed)

	return &processed, nil
}

func getAbbrevDescription(v SourceVideo) string {
	if v.Description == nil {
		return v.SourceURL
	}

	additionalDescription := "\n...\n" + v.SourceURL
	maxLength := 2800 - len(additionalDescription)

	description := strings.TrimSpace(*v.Description)
	if len(description) > maxLength {
		description = description[:maxLength]
	}
	return description + additionalDescription
}

type VideoSource interface {
	GetVideo(id string) (*SourceVideo, error)
	DeleteLocalCache(id string) error
}

type VideoPublisher interface {
	Publish(video PublishableVideo, reflectStream bool) (string, chan error, error)
}
