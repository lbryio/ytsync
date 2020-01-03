package manager

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/lbryio/ytsync/blobs_reflector"
	"github.com/lbryio/ytsync/ip_manager"
	"github.com/lbryio/ytsync/namer"
	"github.com/lbryio/ytsync/sdk"
	logUtils "github.com/lbryio/ytsync/util"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	log "github.com/sirupsen/logrus"
)

type SyncManager struct {
	SyncFlags        sdk.SyncFlags
	maxTries         int
	refill           int
	limit            int
	concurrentJobs   int
	concurrentVideos int
	blobsDir         string
	videosLimit      int
	maxVideoSize     int
	maxVideoLength   float64
	lbrycrdString    string
	awsS3ID          string
	awsS3Secret      string
	awsS3Region      string
	syncStatus       string
	awsS3Bucket      string
	syncProperties   *sdk.SyncProperties
	apiConfig        *sdk.APIConfig
}

func NewSyncManager(syncFlags sdk.SyncFlags, maxTries int, refill int, limit int, concurrentJobs int, concurrentVideos int, blobsDir string, videosLimit int,
	maxVideoSize int, lbrycrdString string, awsS3ID string, awsS3Secret string, awsS3Region string, awsS3Bucket string,
	syncStatus string, syncProperties *sdk.SyncProperties, apiConfig *sdk.APIConfig, maxVideoLength float64) *SyncManager {
	return &SyncManager{
		SyncFlags:        syncFlags,
		maxTries:         maxTries,
		refill:           refill,
		limit:            limit,
		concurrentJobs:   concurrentJobs,
		concurrentVideos: concurrentVideos,
		blobsDir:         blobsDir,
		videosLimit:      videosLimit,
		maxVideoSize:     maxVideoSize,
		maxVideoLength:   maxVideoLength,
		lbrycrdString:    lbrycrdString,
		awsS3ID:          awsS3ID,
		awsS3Secret:      awsS3Secret,
		awsS3Region:      awsS3Region,
		awsS3Bucket:      awsS3Bucket,
		syncStatus:       syncStatus,
		syncProperties:   syncProperties,
		apiConfig:        apiConfig,
	}
}

const (
	StatusPending        = "pending"        // waiting for permission to sync
	StatusPendingEmail   = "pendingemail"   // permission granted but missing email
	StatusQueued         = "queued"         // in sync queue. will be synced soon
	StatusPendingUpgrade = "pendingupgrade" // in sync queue. will be synced soon
	StatusSyncing        = "syncing"        // syncing now
	StatusSynced         = "synced"         // done
	StatusFailed         = "failed"
	StatusFinalized      = "finalized" // no more changes allowed
	StatusAbandoned      = "abandoned" // deleted on youtube or banned
)
const LatestMetadataVersion = 2

var SyncStatuses = []string{StatusPending, StatusPendingEmail, StatusPendingUpgrade, StatusQueued, StatusSyncing, StatusSynced, StatusFailed, StatusFinalized, StatusAbandoned}

const (
	VideoStatusPublished     = "published"
	VideoStatusFailed        = "failed"
	VideoStatusUpgradeFailed = "upgradefailed"
	VideoStatusUnpublished   = "unpublished"
	VideoStatusTranferFailed = "transferfailed"
)

const (
	TransferStateNotTouched = iota
	TransferStatePending
	TransferStateComplete
	TransferStateManual
)

func (s *SyncManager) Start() error {

	if logUtils.ShouldCleanOnStartup() {
		err := logUtils.CleanForStartup()
		if err != nil {
			return err
		}
	}

	syncCount := 0
	for {
		err := s.checkUsedSpace()
		if err != nil {
			return errors.Err(err)
		}

		var syncs []Sync
		shouldInterruptLoop := false

		isSingleChannelSync := s.syncProperties.YoutubeChannelID != ""
		if isSingleChannelSync {
			channels, err := s.apiConfig.FetchChannels("", s.syncProperties)
			if err != nil {
				return errors.Err(err)
			}
			if len(channels) != 1 {
				return errors.Err("Expected 1 channel, %d returned", len(channels))
			}
			lbryChannelName := channels[0].DesiredChannelName
			syncs = make([]Sync, 1)
			syncs[0] = Sync{
				APIConfig:            s.apiConfig,
				YoutubeChannelID:     s.syncProperties.YoutubeChannelID,
				LbryChannelName:      lbryChannelName,
				lbryChannelID:        channels[0].ChannelClaimID,
				MaxTries:             s.maxTries,
				ConcurrentVideos:     s.concurrentVideos,
				Refill:               s.refill,
				Manager:              s,
				LbrycrdString:        s.lbrycrdString,
				AwsS3ID:              s.awsS3ID,
				AwsS3Secret:          s.awsS3Secret,
				AwsS3Region:          s.awsS3Region,
				AwsS3Bucket:          s.awsS3Bucket,
				namer:                namer.NewNamer(),
				Fee:                  channels[0].Fee,
				clientPublishAddress: channels[0].PublishAddress,
				publicKey:            channels[0].PublicKey,
				transferState:        channels[0].TransferState,
			}
			shouldInterruptLoop = true
		} else {
			var queuesToSync []string
			if s.syncStatus != "" {
				queuesToSync = append(queuesToSync, s.syncStatus)
			} else if s.SyncFlags.SyncUpdate {
				queuesToSync = append(queuesToSync, StatusSyncing, StatusSynced)
			} else {
				queuesToSync = append(queuesToSync, StatusSyncing, StatusQueued)
			}
		queues:
			for _, q := range queuesToSync {
				//temporary override for sync-until to give tom the time to review the channels
				if q == StatusQueued {
					s.syncProperties.SyncUntil = time.Now().Add(-8 * time.Hour).Unix()
				}
				channels, err := s.apiConfig.FetchChannels(q, s.syncProperties)
				if err != nil {
					return err
				}
				for i, c := range channels {
					log.Infof("There are %d channels in the \"%s\" queue", len(channels)-i, q)
					syncs = append(syncs, Sync{
						APIConfig:            s.apiConfig,
						YoutubeChannelID:     c.ChannelId,
						LbryChannelName:      c.DesiredChannelName,
						lbryChannelID:        c.ChannelClaimID,
						MaxTries:             s.maxTries,
						ConcurrentVideos:     s.concurrentVideos,
						Refill:               s.refill,
						Manager:              s,
						LbrycrdString:        s.lbrycrdString,
						AwsS3ID:              s.awsS3ID,
						AwsS3Secret:          s.awsS3Secret,
						AwsS3Region:          s.awsS3Region,
						AwsS3Bucket:          s.awsS3Bucket,
						namer:                namer.NewNamer(),
						Fee:                  c.Fee,
						clientPublishAddress: c.PublishAddress,
						publicKey:            c.PublicKey,
						transferState:        c.TransferState,
					})
					if q != StatusFailed {
						continue queues
					}
				}
			}
		}
		if len(syncs) == 0 {
			log.Infoln("No channels to sync. Pausing 5 minutes!")
			time.Sleep(5 * time.Minute)
		}
		for _, sync := range syncs {
			shouldNotCount := false
			logUtils.SendInfoToSlack("Syncing %s (%s) to LBRY! total processed channels since startup: %d", sync.LbryChannelName, sync.YoutubeChannelID, syncCount+1)
			err := sync.FullCycle()
			//TODO: THIS IS A TEMPORARY WORK AROUND FOR THE STUPID IP LOCKUP BUG
			ipPool, _ := ip_manager.GetIPPool(sync.grp)
			if ipPool != nil {
				ipPool.ReleaseAll()
			}

			if err != nil {
				fatalErrors := []string{
					"default_wallet already exists",
					"WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR",
					"NotEnoughFunds",
					"no space left on device",
					"failure uploading wallet",
					"the channel in the wallet is different than the channel in the database",
					"this channel does not belong to this wallet!",
					"You already have a stream claim published under the name",
					"Daily Limit Exceeded",
				}
				if util.SubstringInSlice(err.Error(), fatalErrors) {
					return errors.Prefix("@Nikooo777 this requires manual intervention! Exiting...", err)
				}
				shouldNotCount = strings.Contains(err.Error(), "this youtube channel is being managed by another server")
				if !shouldNotCount {
					logUtils.SendInfoToSlack("A non fatal error was reported by the sync process. %s\nContinuing...", err.Error())
				}
			}
			err = blobs_reflector.ReflectAndClean()
			if err != nil {
				return errors.Prefix("@Nikooo777 something went wrong while reflecting blobs", err)
			}
			logUtils.SendInfoToSlack("Syncing %s (%s) reached an end. total processed channels since startup: %d", sync.LbryChannelName, sync.YoutubeChannelID, syncCount+1)
			if !shouldNotCount {
				syncCount++
			}
			if sync.IsInterrupted() || (s.limit != 0 && syncCount >= s.limit) {
				shouldInterruptLoop = true
				break
			}
		}
		if shouldInterruptLoop || s.SyncFlags.SingleRun {
			break
		}
	}
	return nil
}
func (s *SyncManager) GetS3AWSConfig() aws.Config {
	return aws.Config{
		Credentials: credentials.NewStaticCredentials(s.awsS3ID, s.awsS3Secret, ""),
		Region:      &s.awsS3Region,
	}
}
func (s *SyncManager) checkUsedSpace() error {
	usedPctile, err := GetUsedSpace(logUtils.GetBlobsDir())
	if err != nil {
		return errors.Err(err)
	}
	if usedPctile >= 0.90 && !s.SyncFlags.SkipSpaceCheck {
		return errors.Err(fmt.Sprintf("more than 90%% of the space has been used. use --skip-space-check to ignore. Used: %.1f%%", usedPctile*100))
	}
	log.Infof("disk usage: %.1f%%", usedPctile*100)
	return nil
}

// GetUsedSpace returns a value between 0 and 1, with 0 being completely empty and 1 being full, for the disk that holds the provided path
func GetUsedSpace(path string) (float32, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, err
	}
	// Available blocks * size per block = available space in bytes
	all := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := all - free

	return float32(used) / float32(all), nil
}
