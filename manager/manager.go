package manager

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/lbryio/ytsync/v5/blobs_reflector"
	"github.com/lbryio/ytsync/v5/ip_manager"
	"github.com/lbryio/ytsync/v5/namer"
	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/lbryio/ytsync/v5/shared"
	logUtils "github.com/lbryio/ytsync/v5/util"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/util"

	log "github.com/sirupsen/logrus"
)

type SyncManager struct {
	CliFlags   shared.SyncFlags
	ApiConfig  *sdk.APIConfig
	LbrycrdDsn string
	AwsConfigs *shared.AwsConfigs

	blobsDir       string
	channelsToSync []Sync
}

func NewSyncManager(cliFlags shared.SyncFlags, blobsDir, lbrycrdDsn string, awsConfigs *shared.AwsConfigs, apiConfig *sdk.APIConfig) *SyncManager {
	return &SyncManager{
		CliFlags:   cliFlags,
		blobsDir:   blobsDir,
		LbrycrdDsn: lbrycrdDsn,
		AwsConfigs: awsConfigs,
		ApiConfig:  apiConfig,
	}
}
func (s *SyncManager) enqueueChannel(channel *shared.YoutubeChannel) {
	s.channelsToSync = append(s.channelsToSync, Sync{
		DbChannelData: channel,
		Manager:       s,
		namer:         namer.NewNamer(),
	})
}

func (s *SyncManager) Start() error {
	if logUtils.ShouldCleanOnStartup() {
		err := logUtils.CleanForStartup()
		if err != nil {
			return err
		}
	}

	var lastChannelProcessed string
	var secondLastChannelProcessed string
	syncCount := 0
	for {
		s.channelsToSync = make([]Sync, 0, 10) // reset sync queue
		err := s.checkUsedSpace()
		if err != nil {
			return errors.Err(err)
		}
		shouldInterruptLoop := false

		if s.CliFlags.IsSingleChannelSync() {
			channels, err := s.ApiConfig.FetchChannels("", &s.CliFlags)
			if err != nil {
				return errors.Err(err)
			}
			if len(channels) != 1 {
				return errors.Err("Expected 1 channel, %d returned", len(channels))
			}
			s.enqueueChannel(&channels[0])
			shouldInterruptLoop = true
		} else {
			var queuesToSync []string
			if s.CliFlags.Status != "" {
				queuesToSync = append(queuesToSync, s.CliFlags.Status)
			} else if s.CliFlags.SyncUpdate {
				queuesToSync = append(queuesToSync, shared.StatusSyncing, shared.StatusSynced)
			} else {
				queuesToSync = append(queuesToSync, shared.StatusSyncing, shared.StatusQueued)
			}
			if s.CliFlags.SecondaryStatus != "" {
				queuesToSync = append(queuesToSync, s.CliFlags.SecondaryStatus)
			}
		queues:
			for _, q := range queuesToSync {
				channels, err := s.ApiConfig.FetchChannels(q, &s.CliFlags)
				if err != nil {
					return err
				}
				log.Infof("Currently processing the \"%s\" queue with %d channels", q, len(channels))
				for _, c := range channels {
					s.enqueueChannel(&c)
					queueAll := q == shared.StatusFailed || q == shared.StatusSyncing
					if !queueAll {
						break queues
					}
				}
				log.Infof("Drained the \"%s\" queue", q)
			}
		}
		if len(s.channelsToSync) == 0 {
			log.Infoln("No channels to sync. Pausing 5 minutes!")
			time.Sleep(5 * time.Minute)
		}
		for _, sync := range s.channelsToSync {
			if lastChannelProcessed == sync.DbChannelData.ChannelId && secondLastChannelProcessed == lastChannelProcessed {
				util.SendToSlack("We just killed a sync for %s to stop looping! (%s)", sync.DbChannelData.DesiredChannelName, sync.DbChannelData.ChannelId)
				stopTheLoops := errors.Err("Found channel %s running 3 times, set it to failed, and reprocess later", sync.DbChannelData.DesiredChannelName)
				sync.setChannelTerminationStatus(&stopTheLoops)
				continue
			}
			secondLastChannelProcessed = lastChannelProcessed
			lastChannelProcessed = sync.DbChannelData.ChannelId
			shouldNotCount := false
			logUtils.SendInfoToSlack("Syncing %s (%s) to LBRY! total processed channels since startup: %d", sync.DbChannelData.DesiredChannelName, sync.DbChannelData.ChannelId, syncCount+1)
			err := sync.FullCycle()
			//TODO: THIS IS A TEMPORARY WORK AROUND FOR THE STUPID IP LOCKUP BUG
			ipPool, _ := ip_manager.GetIPPool(sync.grp)
			if ipPool != nil {
				ipPool.ReleaseAll()
			}

			if err != nil {
				if strings.Contains(err.Error(), "quotaExceeded") {
					logUtils.SleepUntilQuotaReset()
				}
				fatalErrors := []string{
					"default_wallet already exists",
					"WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR",
					"NotEnoughFunds",
					"no space left on device",
					"failure uploading wallet",
					"the channel in the wallet is different than the channel in the database",
					"this channel does not belong to this wallet!",
					"You already have a stream claim published under the name",
				}

				if util.SubstringInSlice(err.Error(), fatalErrors) {
					return errors.Prefix("@Nikooo777 this requires manual intervention! Exiting...", err)
				}
				shouldNotCount = strings.Contains(err.Error(), "this youtube channel is being managed by another server")
				if !shouldNotCount {
					logUtils.SendInfoToSlack("A non fatal error was reported by the sync process.\n%s", errors.FullTrace(err))
				}
			}
			err = blobs_reflector.ReflectAndClean()
			if err != nil {
				return errors.Prefix("@Nikooo777 something went wrong while reflecting blobs", err)
			}
			logUtils.SendInfoToSlack("%s (%s) reached an end. Total processed channels since startup: %d", sync.DbChannelData.DesiredChannelName, sync.DbChannelData.ChannelId, syncCount+1)
			if !shouldNotCount {
				syncCount++
			}
			if sync.IsInterrupted() || (s.CliFlags.Limit != 0 && syncCount >= s.CliFlags.Limit) {
				shouldInterruptLoop = true
				break
			}
		}
		if shouldInterruptLoop || s.CliFlags.SingleRun {
			break
		}
	}
	return nil
}

func (s *SyncManager) checkUsedSpace() error {
	usedPctile, err := GetUsedSpace(logUtils.GetBlobsDir())
	if err != nil {
		return errors.Err(err)
	}
	if usedPctile >= 0.90 && !s.CliFlags.SkipSpaceCheck {
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
