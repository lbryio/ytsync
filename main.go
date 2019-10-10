package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/util"
	"github.com/lbryio/ytsync/manager"
	"github.com/lbryio/ytsync/sdk"
	ytUtils "github.com/lbryio/ytsync/util"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var Version string

const defaultMaxTries = 3

var (
	flags          sdk.SyncFlags
	maxTries       int
	refill         int
	limit          int
	syncStatus     string
	channelID      string
	syncFrom       int64
	syncUntil      int64
	concurrentJobs int
	videosLimit    int
	maxVideoSize   int
	maxVideoLength float64
)

func main() {
	rand.Seed(time.Now().UnixNano())
	log.SetLevel(log.DebugLevel)

	cmd := &cobra.Command{
		Use:   "ytsync",
		Short: "Publish youtube channels into LBRY network automatically.",
		Run:   ytSync,
		Args:  cobra.RangeArgs(0, 0),
	}

	cmd.Flags().BoolVar(&flags.StopOnError, "stop-on-error", false, "If a publish fails, stop all publishing and exit")
	cmd.Flags().IntVar(&maxTries, "max-tries", defaultMaxTries, "Number of times to try a publish that fails")
	cmd.Flags().BoolVar(&flags.TakeOverExistingChannel, "takeover-existing-channel", false, "If channel exists and we don't own it, take over the channel")
	cmd.Flags().IntVar(&limit, "limit", 0, "limit the amount of channels to sync")
	cmd.Flags().BoolVar(&flags.SkipSpaceCheck, "skip-space-check", false, "Do not perform free space check on startup")
	cmd.Flags().BoolVar(&flags.SyncUpdate, "update", false, "Update previously synced channels instead of syncing new ones")
	cmd.Flags().BoolVar(&flags.SingleRun, "run-once", false, "Whether the process should be stopped after one cycle or not")
	cmd.Flags().BoolVar(&flags.RemoveDBUnpublished, "remove-db-unpublished", false, "Remove videos from the database that are marked as published but aren't really published")
	cmd.Flags().BoolVar(&flags.UpgradeMetadata, "upgrade-metadata", false, "Upgrade videos if they're on the old metadata version")
	cmd.Flags().BoolVar(&flags.DisableTransfers, "no-transfers", false, "Skips the transferring process of videos, channels and supports")
	cmd.Flags().StringVar(&syncStatus, "status", "", "Specify which queue to pull from. Overrides --update")
	cmd.Flags().StringVar(&channelID, "channelID", "", "If specified, only this channel will be synced.")
	cmd.Flags().Int64Var(&syncFrom, "after", time.Unix(0, 0).Unix(), "Specify from when to pull jobs [Unix time](Default: 0)")
	cmd.Flags().Int64Var(&syncUntil, "before", time.Now().AddDate(1, 0, 0).Unix(), "Specify until when to pull jobs [Unix time](Default: current Unix time)")
	cmd.Flags().IntVar(&concurrentJobs, "concurrent-jobs", 1, "how many jobs to process concurrently")
	cmd.Flags().IntVar(&videosLimit, "videos-limit", 1000, "how many videos to process per channel")
	cmd.Flags().IntVar(&maxVideoSize, "max-size", 2048, "Maximum video size to process (in MB)")
	cmd.Flags().Float64Var(&maxVideoLength, "max-length", 2.0, "Maximum video length to process (in hours)")

	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
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

	if syncStatus != "" && !util.InSlice(syncStatus, manager.SyncStatuses) {
		log.Errorf("status must be one of the following: %v\n", manager.SyncStatuses)
		return
	}

	if flags.StopOnError && maxTries != defaultMaxTries {
		log.Errorln("--stop-on-error and --max-tries are mutually exclusive")
		return
	}
	if maxTries < 1 {
		log.Errorln("setting --max-tries less than 1 doesn't make sense")
		return
	}

	if limit < 0 {
		log.Errorln("setting --limit less than 0 (unlimited) doesn't make sense")
		return
	}

	apiURL := os.Getenv("LBRY_WEB_API")
	apiToken := os.Getenv("LBRY_API_TOKEN")
	youtubeAPIKey := os.Getenv("YOUTUBE_API_KEY")
	lbrycrdString := os.Getenv("LBRYCRD_STRING")
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
	if lbrycrdString == "" {
		log.Infoln("Using default (local) lbrycrd instance. Set LBRYCRD_STRING if you want to use something else")
	}

	blobsDir := ytUtils.GetBlobsDir()

	syncProperties := &sdk.SyncProperties{
		SyncFrom:         syncFrom,
		SyncUntil:        syncUntil,
		YoutubeChannelID: channelID,
	}
	apiConfig := &sdk.APIConfig{
		YoutubeAPIKey: youtubeAPIKey,
		ApiURL:        apiURL,
		ApiToken:      apiToken,
		HostName:      hostname,
	}
	sm := manager.NewSyncManager(
		flags,
		maxTries,
		refill,
		limit,
		concurrentJobs,
		concurrentJobs,
		blobsDir,
		videosLimit,
		maxVideoSize,
		lbrycrdString,
		awsS3ID,
		awsS3Secret,
		awsS3Region,
		awsS3Bucket,
		syncStatus,
		syncProperties,
		apiConfig,
		maxVideoLength,
	)
	err := sm.Start()
	if err != nil {
		ytUtils.SendErrorToSlack(errors.FullTrace(err))
	}
	ytUtils.SendInfoToSlack("Syncing process terminated!")
}
