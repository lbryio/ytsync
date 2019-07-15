package manager

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/lbryio/ytsync/blobs_reflector"
	"github.com/lbryio/ytsync/namer"
	"github.com/lbryio/ytsync/sdk"
	logUtils "github.com/lbryio/ytsync/util"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	log "github.com/sirupsen/logrus"
)

type SyncManager struct {
	stopOnError             bool
	maxTries                int
	takeOverExistingChannel bool
	refill                  int
	limit                   int
	skipSpaceCheck          bool
	syncUpdate              bool
	concurrentJobs          int
	concurrentVideos        int
	blobsDir                string
	videosLimit             int
	maxVideoSize            int
	maxVideoLength          float64
	lbrycrdString           string
	awsS3ID                 string
	awsS3Secret             string
	awsS3Region             string
	syncStatus              string
	awsS3Bucket             string
	singleRun               bool
	syncProperties          *sdk.SyncProperties
	apiConfig               *sdk.APIConfig
	removeDBUnpublished     bool
	upgradeMetadata         bool
}

func NewSyncManager(stopOnError bool, maxTries int, takeOverExistingChannel bool, refill int, limit int,
	skipSpaceCheck bool, syncUpdate bool, concurrentJobs int, concurrentVideos int, blobsDir string, videosLimit int,
	maxVideoSize int, lbrycrdString string, awsS3ID string, awsS3Secret string, awsS3Region string, awsS3Bucket string,
	syncStatus string, singleRun bool, syncProperties *sdk.SyncProperties, apiConfig *sdk.APIConfig, maxVideoLength float64, removeDBUnpublished bool, upgradeMetadata bool) *SyncManager {
	return &SyncManager{
		stopOnError:             stopOnError,
		maxTries:                maxTries,
		takeOverExistingChannel: takeOverExistingChannel,
		refill:                  refill,
		limit:                   limit,
		skipSpaceCheck:          skipSpaceCheck,
		syncUpdate:              syncUpdate,
		concurrentJobs:          concurrentJobs,
		concurrentVideos:        concurrentVideos,
		blobsDir:                blobsDir,
		videosLimit:             videosLimit,
		maxVideoSize:            maxVideoSize,
		maxVideoLength:          maxVideoLength,
		lbrycrdString:           lbrycrdString,
		awsS3ID:                 awsS3ID,
		awsS3Secret:             awsS3Secret,
		awsS3Region:             awsS3Region,
		awsS3Bucket:             awsS3Bucket,
		syncStatus:              syncStatus,
		singleRun:               singleRun,
		syncProperties:          syncProperties,
		apiConfig:               apiConfig,
		removeDBUnpublished:     removeDBUnpublished,
		upgradeMetadata:         upgradeMetadata,
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

var SyncStatuses = []string{StatusPending, StatusPendingEmail, StatusPendingUpgrade, StatusQueued, StatusSyncing, StatusSynced, StatusFailed, StatusFinalized, StatusAbandoned}

const (
	VideoStatusPublished     = "published"
	VideoStatusFailed        = "failed"
	VideoStatusUpgradeFailed = "upgradefailed"
	VideoStatusUnpublished   = "unpublished"
)

func (s *SyncManager) Start() error {

	syncCount := 0
	for {
		err := s.checkUsedSpace()
		if err != nil {
			return err
		}

		var syncs []Sync
		shouldInterruptLoop := false

		isSingleChannelSync := s.syncProperties.YoutubeChannelID != ""
		if isSingleChannelSync {
			channels, err := s.apiConfig.FetchChannels("", s.syncProperties)
			if err != nil {
				return err
			}
			if len(channels) != 1 {
				return errors.Err("Expected 1 channel, %d returned", len(channels))
			}
			lbryChannelName := channels[0].DesiredChannelName
			syncs = make([]Sync, 1)
			syncs[0] = Sync{
				APIConfig:               s.apiConfig,
				YoutubeChannelID:        s.syncProperties.YoutubeChannelID,
				LbryChannelName:         lbryChannelName,
				lbryChannelID:           channels[0].ChannelClaimID,
				StopOnError:             s.stopOnError,
				MaxTries:                s.maxTries,
				ConcurrentVideos:        s.concurrentVideos,
				TakeOverExistingChannel: s.takeOverExistingChannel,
				Refill:                  s.refill,
				Manager:                 s,
				LbrycrdString:           s.lbrycrdString,
				AwsS3ID:                 s.awsS3ID,
				AwsS3Secret:             s.awsS3Secret,
				AwsS3Region:             s.awsS3Region,
				AwsS3Bucket:             s.awsS3Bucket,
				namer:                   namer.NewNamer(),
				Fee:                     channels[0].Fee,
			}
			shouldInterruptLoop = true
		} else {
			var queuesToSync []string
			//TODO: implement scrambling to avoid starvation of queues
			if s.syncStatus != "" {
				queuesToSync = append(queuesToSync, s.syncStatus)
			} else if s.syncUpdate {
				queuesToSync = append(queuesToSync, StatusSyncing, StatusSynced)
			} else {
				queuesToSync = append(queuesToSync, StatusSyncing, StatusQueued)
			}
			for _, q := range queuesToSync {
				channels, err := s.apiConfig.FetchChannels(q, s.syncProperties)
				if err != nil {
					return err
				}
				log.Infof("There are %d channels in the \"%s\" queue", len(channels), q)
				if len(channels) > 0 {
					c := channels[0]
					syncs = append(syncs, Sync{
						APIConfig:               s.apiConfig,
						YoutubeChannelID:        c.ChannelId,
						LbryChannelName:         c.DesiredChannelName,
						lbryChannelID:           c.ChannelClaimID,
						StopOnError:             s.stopOnError,
						MaxTries:                s.maxTries,
						ConcurrentVideos:        s.concurrentVideos,
						TakeOverExistingChannel: s.takeOverExistingChannel,
						Refill:                  s.refill,
						Manager:                 s,
						LbrycrdString:           s.lbrycrdString,
						AwsS3ID:                 s.awsS3ID,
						AwsS3Secret:             s.awsS3Secret,
						AwsS3Region:             s.awsS3Region,
						AwsS3Bucket:             s.awsS3Bucket,
						namer:                   namer.NewNamer(),
						Fee:                     c.Fee,
					})
					continue
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
			if err != nil {
				fatalErrors := []string{
					"default_wallet already exists",
					"WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR",
					"NotEnoughFunds",
					"no space left on device",
					"failure uploading wallet",
					"the channel in the wallet is different than the channel in the database",
					"this channel does not belong to this wallet!",
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
		if shouldInterruptLoop || s.singleRun {
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
	usedPctile, err := GetUsedSpace(s.blobsDir)
	if err != nil {
		return err
	}
	if usedPctile >= 0.90 && !s.skipSpaceCheck {
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
