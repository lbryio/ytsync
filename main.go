package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/lbryio/ytsync/v5/configs"
	"github.com/lbryio/ytsync/v5/manager"
	"github.com/lbryio/ytsync/v5/shared"
	ytUtils "github.com/lbryio/ytsync/v5/util"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/util"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var Version string

const defaultMaxTries = 3

var (
	cliFlags       shared.SyncFlags
	maxVideoLength int
)

func main() {
	rand.Seed(time.Now().UnixNano())
	log.SetLevel(log.DebugLevel)
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Error(http.ListenAndServe(":2112", nil))
	}()
	cmd := &cobra.Command{
		Use:   "ytsync",
		Short: "Publish youtube channels into LBRY network automatically.",
		Run:   ytSync,
		Args:  cobra.RangeArgs(0, 0),
	}

	cmd.Flags().IntVar(&cliFlags.MaxTries, "max-tries", defaultMaxTries, "Number of times to try a publish that fails")
	cmd.Flags().BoolVar(&cliFlags.TakeOverExistingChannel, "takeover-existing-channel", false, "If channel exists and we don't own it, take over the channel")
	cmd.Flags().IntVar(&cliFlags.Limit, "limit", 0, "limit the amount of channels to sync")
	cmd.Flags().BoolVar(&cliFlags.SkipSpaceCheck, "skip-space-check", false, "Do not perform free space check on startup")
	cmd.Flags().BoolVar(&cliFlags.SyncUpdate, "update", false, "Update previously synced channels instead of syncing new ones")
	cmd.Flags().BoolVar(&cliFlags.SingleRun, "run-once", false, "Whether the process should be stopped after one cycle or not")
	cmd.Flags().BoolVar(&cliFlags.RemoveDBUnpublished, "remove-db-unpublished", false, "Remove videos from the database that are marked as published but aren't really published")
	cmd.Flags().BoolVar(&cliFlags.UpgradeMetadata, "upgrade-metadata", false, "Upgrade videos if they're on the old metadata version")
	cmd.Flags().BoolVar(&cliFlags.DisableTransfers, "no-transfers", false, "Skips the transferring process of videos, channels and supports")
	cmd.Flags().BoolVar(&cliFlags.QuickSync, "quick", false, "Look up only the last 50 videos from youtube")
	cmd.Flags().StringVar(&cliFlags.Status, "status", "", "Specify which queue to pull from. Overrides --update")
	cmd.Flags().StringVar(&cliFlags.SecondaryStatus, "status2", "", "Specify which secondary queue to pull from.")
	cmd.Flags().StringVar(&cliFlags.ChannelID, "channelID", "", "If specified, only this channel will be synced.")
	cmd.Flags().Int64Var(&cliFlags.SyncFrom, "after", time.Unix(0, 0).Unix(), "Specify from when to pull jobs [Unix time](Default: 0)")
	cmd.Flags().Int64Var(&cliFlags.SyncUntil, "before", time.Now().AddDate(1, 0, 0).Unix(), "Specify until when to pull jobs [Unix time](Default: current Unix time)")
	cmd.Flags().IntVar(&cliFlags.ConcurrentJobs, "concurrent-jobs", 1, "how many jobs to process concurrently")
	cmd.Flags().IntVar(&cliFlags.VideosLimit, "videos-limit", 0, "how many videos to process per channel (leave 0 for automatic detection)")
	cmd.Flags().IntVar(&cliFlags.MaxVideoSize, "max-size", 2048, "Maximum video size to process (in MB)")
	cmd.Flags().IntVar(&maxVideoLength, "max-length", 2, "Maximum video length to process (in hours)")

	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func ytSync(cmd *cobra.Command, args []string) {
	err := configs.Init("./config.json")
	if err != nil {
		log.Fatalf("could not parse configuration file: %s", errors.FullTrace(err))
	}

	if configs.Configuration.SlackToken == "" {
		log.Error("A slack token was not present in the config! Slack messages disabled!")
	} else {
		util.InitSlack(configs.Configuration.SlackToken, configs.Configuration.SlackChannel, configs.Configuration.GetHostname())
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

	if configs.Configuration.InternalApisEndpoint == "" {
		log.Errorln("An Internal APIs Endpoint was not defined")
		return
	}
	if configs.Configuration.InternalApisAuthToken == "" {
		log.Errorln("An Internal APIs auth token was not defined")
		return
	}
	if configs.Configuration.WalletS3Config.ID == "" || configs.Configuration.WalletS3Config.Region == "" || configs.Configuration.WalletS3Config.Bucket == "" || configs.Configuration.WalletS3Config.Secret == "" || configs.Configuration.WalletS3Config.Endpoint == "" {
		log.Errorln("Wallet S3 configuration is incomplete")
		return
	}
	if configs.Configuration.BlockchaindbS3Config.ID == "" || configs.Configuration.BlockchaindbS3Config.Region == "" || configs.Configuration.BlockchaindbS3Config.Bucket == "" || configs.Configuration.BlockchaindbS3Config.Secret == "" || configs.Configuration.BlockchaindbS3Config.Endpoint == "" {
		log.Errorln("Blockchain DBs S3 configuration is incomplete")
		return
	}
	if configs.Configuration.LbrycrdString == "" {
		log.Infoln("Using default (local) lbrycrd instance. Set lbrycrd_string if you want to use something else")
	}

	blobsDir := ytUtils.GetBlobsDir()

	sm := manager.NewSyncManager(
		cliFlags,
		blobsDir,
	)
	err = sm.Start()
	if err != nil {
		ytUtils.SendErrorToSlack(errors.FullTrace(err))
	}
	ytUtils.SendInfoToSlack("Syncing process terminated!")
}
