package cmd

import (
	"os"
	"time"

	"github.com/lbryio/ytsync/v5/manager"
	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/lbryio/ytsync/v5/shared"
	ytUtils "github.com/lbryio/ytsync/v5/util"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/util"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const defaultMaxTries = 3

var (
	cliFlags       shared.SyncFlags
	maxVideoLength int
)

var rootCmd = &cobra.Command{
	Use:   "ytsync",
	Short: "Publish youtube channels into LBRY network automatically.",
	Run:   ytSync,
	Args:  cobra.RangeArgs(0, 0),
}

func init() {
	rootCmd.Flags().IntVar(&cliFlags.MaxTries, "max-tries", defaultMaxTries, "Number of times to try a publish that fails")
	rootCmd.Flags().BoolVar(&cliFlags.TakeOverExistingChannel, "takeover-existing-channel", false, "If channel exists and we don't own it, take over the channel")
	rootCmd.Flags().IntVar(&cliFlags.Limit, "limit", 0, "limit the amount of channels to sync")
	rootCmd.Flags().BoolVar(&cliFlags.SkipSpaceCheck, "skip-space-check", false, "Do not perform free space check on startup")
	rootCmd.Flags().BoolVar(&cliFlags.SyncUpdate, "update", false, "Update previously synced channels instead of syncing new ones")
	rootCmd.Flags().BoolVar(&cliFlags.SingleRun, "run-once", false, "Whether the process should be stopped after one cycle or not")
	rootCmd.Flags().BoolVar(&cliFlags.RemoveDBUnpublished, "remove-db-unpublished", false, "Remove videos from the database that are marked as published but aren't really published")
	rootCmd.Flags().BoolVar(&cliFlags.UpgradeMetadata, "upgrade-metadata", false, "Upgrade videos if they're on the old metadata version")
	rootCmd.Flags().BoolVar(&cliFlags.DisableTransfers, "no-transfers", false, "Skips the transferring process of videos, channels and supports")
	rootCmd.Flags().BoolVar(&cliFlags.QuickSync, "quick", false, "Look up only the last 50 videos from youtube")
	rootCmd.Flags().StringVar(&cliFlags.Status, "status", "", "Specify which queue to pull from. Overrides --update")
	rootCmd.Flags().StringVar(&cliFlags.SecondaryStatus, "status2", "", "Specify which secondary queue to pull from.")
	rootCmd.Flags().StringVar(&cliFlags.ChannelID, "channelID", "", "If specified, only this channel will be synced.")
	rootCmd.Flags().Int64Var(&cliFlags.SyncFrom, "after", time.Unix(0, 0).Unix(), "Specify from when to pull jobs [Unix time](Default: 0)")
	rootCmd.Flags().Int64Var(&cliFlags.SyncUntil, "before", time.Now().AddDate(1, 0, 0).Unix(), "Specify until when to pull jobs [Unix time](Default: current Unix time)")
	rootCmd.Flags().IntVar(&cliFlags.ConcurrentJobs, "concurrent-jobs", 1, "how many jobs to process concurrently")
	rootCmd.Flags().IntVar(&cliFlags.VideosLimit, "videos-limit", 0, "how many videos to process per channel (leave 0 for automatic detection)")
	rootCmd.Flags().IntVar(&cliFlags.MaxVideoSize, "max-size", 2048, "Maximum video size to process (in MB)")
	rootCmd.Flags().IntVar(&maxVideoLength, "max-length", 2, "Maximum video length to process (in hours)")
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		log.Errorln(err)
		os.Exit(1)
	}
}

func ytSync(cmd *cobra.Command, args []string) {
	var hostname string
	slackToken := os.Getenv("SLACK_TOKEN")
	if slackToken == "" {
		log.Error("A slack token was not present in env vars! Slack messages disabled!")
	} else {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			log.Error("could not detect system hostname")
			hostname = "ytsync-unknown"
		}
		if len(hostname) > 30 {
			hostname = hostname[0:30]
		}

		util.InitSlack(os.Getenv("SLACK_TOKEN"), os.Getenv("SLACK_CHANNEL"), hostname)
	}

	if cliFlags.Status != "" && !util.InSlice(cliFlags.Status, shared.SyncStatuses) {
		log.Errorf("status must be one of the following: %v\n", shared.SyncStatuses)
		return
	}

	if cliFlags.MaxTries < 1 {
		log.Errorln("setting --max-tries less than 1 doesn't make sense")
		return
	}

	if cliFlags.Limit < 0 {
		log.Errorln("setting --limit less than 0 (unlimited) doesn't make sense")
		return
	}
	cliFlags.MaxVideoLength = time.Duration(maxVideoLength) * time.Hour

	apiURL := os.Getenv("LBRY_WEB_API")
	apiToken := os.Getenv("LBRY_API_TOKEN")
	youtubeAPIKey := os.Getenv("YOUTUBE_API_KEY")
	lbrycrdDsn := os.Getenv("LBRYCRD_STRING")
	awsS3ID := os.Getenv("AWS_S3_ID")
	awsS3Secret := os.Getenv("AWS_S3_SECRET")
	awsS3Region := os.Getenv("AWS_S3_REGION")
	awsS3Bucket := os.Getenv("AWS_S3_BUCKET")
	if apiURL == "" {
		log.Errorln("An API URL was not defined. Please set the environment variable LBRY_WEB_API")
		return
	}
	if apiToken == "" {
		log.Errorln("An API Token was not defined. Please set the environment variable LBRY_API_TOKEN")
		return
	}
	if youtubeAPIKey == "" {
		log.Errorln("A Youtube API key was not defined. Please set the environment variable YOUTUBE_API_KEY")
		return
	}
	if awsS3ID == "" {
		log.Errorln("AWS S3 ID credentials were not defined. Please set the environment variable AWS_S3_ID")
		return
	}
	if awsS3Secret == "" {
		log.Errorln("AWS S3 Secret credentials were not defined. Please set the environment variable AWS_S3_SECRET")
		return
	}
	if awsS3Region == "" {
		log.Errorln("AWS S3 Region was not defined. Please set the environment variable AWS_S3_REGION")
		return
	}
	if awsS3Bucket == "" {
		log.Errorln("AWS S3 Bucket was not defined. Please set the environment variable AWS_S3_BUCKET")
		return
	}
	if lbrycrdDsn == "" {
		log.Infoln("Using default (local) lbrycrd instance. Set LBRYCRD_STRING if you want to use something else")
	}

	blobsDir := ytUtils.GetBlobsDir()

	apiConfig := &sdk.APIConfig{
		YoutubeAPIKey: youtubeAPIKey,
		ApiURL:        apiURL,
		ApiToken:      apiToken,
		HostName:      hostname,
	}
	awsConfig := &shared.AwsConfigs{
		AwsS3ID:     awsS3ID,
		AwsS3Secret: awsS3Secret,
		AwsS3Region: awsS3Region,
		AwsS3Bucket: awsS3Bucket,
	}
	sm := manager.NewSyncManager(
		cliFlags,
		blobsDir,
		lbrycrdDsn,
		awsConfig,
		apiConfig,
	)

	err := sm.Start()
	if err != nil {
		ytUtils.SendErrorToSlack(errors.FullTrace(err))
	}

	ytUtils.SendInfoToSlack("Syncing process terminated!")
}
