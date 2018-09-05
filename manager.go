package ytsync

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/null"
	"github.com/lbryio/lbry.go/util"
	"github.com/lbryio/lbry.go/ytsync/sdk"
	log "github.com/sirupsen/logrus"
)

type SyncManager struct {
	StopOnError             bool
	MaxTries                int
	TakeOverExistingChannel bool
	Refill                  int
	Limit                   int
	SkipSpaceCheck          bool
	SyncUpdate              bool
	ConcurrentJobs          int
	ConcurrentVideos        int
	BlobsDir                string
	VideosLimit             int
	MaxVideoSize            int
	LbrycrdString           string
	AwsS3ID                 string
	AwsS3Secret             string
	AwsS3Region             string
	SyncStatus              string
	AwsS3Bucket             string
	SingleRun               bool
	ChannelProperties       *sdk.ChannelProperties
	APIConfig               *sdk.APIConfig
}

const (
	StatusPending   = "pending" // waiting for permission to sync
	StatusQueued    = "queued"  // in sync queue. will be synced soon
	StatusSyncing   = "syncing" // syncing now
	StatusSynced    = "synced"  // done
	StatusFailed    = "failed"
	StatusFinalized = "finalized" // no more changes allowed
)

var SyncStatuses = []string{StatusPending, StatusQueued, StatusSyncing, StatusSynced, StatusFailed, StatusFinalized}

type apiJobsResponse struct {
	Success bool                `json:"success"`
	Error   null.String         `json:"error"`
	Data    []apiYoutubeChannel `json:"data"`
}

type apiYoutubeChannel struct {
	ChannelId          string      `json:"channel_id"`
	TotalVideos        uint        `json:"total_videos"`
	DesiredChannelName string      `json:"desired_channel_name"`
	SyncServer         null.String `json:"sync_server"`
	Fee                *struct {
		Amount   string `json:"amount"`
		Address  string `json:"address"`
		Currency string `json:"currency"`
	} `json:"fee"`
}

type apiChannelStatusResponse struct {
	Success bool          `json:"success"`
	Error   null.String   `json:"error"`
	Data    []syncedVideo `json:"data"`
}

type syncedVideo struct {
	VideoID       string `json:"video_id"`
	Published     bool   `json:"published"`
	FailureReason string `json:"failure_reason"`
	ClaimName     string `json:"claim_name"`
}

const (
	VideoStatusPublished = "published"
	VideoStatusFailed    = "failed"
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

		isSingleChannelSync := s.ChannelProperties.YoutubeChannelID != ""
		if isSingleChannelSync {
			channels, err := s.APIConfig.FetchChannels("", s.ChannelProperties)
			if err != nil {
				return err
			}
			if len(channels) != 1 {
				return errors.Err("Expected 1 channel, %d returned", len(channels))
			}
			lbryChannelName := channels[0].DesiredChannelName
			syncs = make([]Sync, 1)
			syncs[0] = Sync{
				YoutubeAPIKey:           s.YoutubeAPIKey,
				YoutubeChannelID:        s.YoutubeChannelID,
				LbryChannelName:         lbryChannelName,
				StopOnError:             s.StopOnError,
				MaxTries:                s.MaxTries,
				ConcurrentVideos:        s.ConcurrentVideos,
				TakeOverExistingChannel: s.TakeOverExistingChannel,
				Refill:                  s.Refill,
				Manager:                 s,
				LbrycrdString:           s.LbrycrdString,
				AwsS3ID:                 s.AwsS3ID,
				AwsS3Secret:             s.AwsS3Secret,
				AwsS3Region:             s.AwsS3Region,
				AwsS3Bucket:             s.AwsS3Bucket,
			}
			shouldInterruptLoop = true
		} else {
			var queuesToSync []string
			if s.SyncStatus != "" {
				queuesToSync = append(queuesToSync, s.SyncStatus)
			} else if s.SyncUpdate {
				queuesToSync = append(queuesToSync, StatusSyncing, StatusSynced)
			} else {
				queuesToSync = append(queuesToSync, StatusSyncing, StatusQueued)
			}
			for _, q := range queuesToSync {
				channels, err := s.APIConfig.FetchChannels(q, s.ChannelProperties)
				if err != nil {
					return err
				}
				for _, c := range channels {
					syncs = append(syncs, Sync{
						YoutubeAPIKey:           s.YoutubeAPIKey,
						YoutubeChannelID:        c.ChannelId,
						LbryChannelName:         c.DesiredChannelName,
						StopOnError:             s.StopOnError,
						MaxTries:                s.MaxTries,
						ConcurrentVideos:        s.ConcurrentVideos,
						TakeOverExistingChannel: s.TakeOverExistingChannel,
						Refill:                  s.Refill,
						Manager:                 s,
						LbrycrdString:           s.LbrycrdString,
						AwsS3ID:                 s.AwsS3ID,
						AwsS3Secret:             s.AwsS3Secret,
						AwsS3Region:             s.AwsS3Region,
						AwsS3Bucket:             s.AwsS3Bucket,
					})
				}
			}
		}
		if len(syncs) == 0 {
			log.Infoln("No channels to sync. Pausing 5 minutes!")
			time.Sleep(5 * time.Minute)
		}
		for i, sync := range syncs {
			shouldNotCount := false
			SendInfoToSlack("Syncing %s (%s) to LBRY! (iteration %d/%d - total processed channels: %d)", sync.LbryChannelName, sync.YoutubeChannelID, i+1, len(syncs), syncCount+1)
			err := sync.FullCycle()
			if err != nil {
				fatalErrors := []string{
					"default_wallet already exists",
					"WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR",
					"NotEnoughFunds",
					"no space left on device",
					"failure uploading wallet",
				}
				if util.SubstringInSlice(err.Error(), fatalErrors) {
					return errors.Prefix("@Nikooo777 this requires manual intervention! Exiting...", err)
				}
				shouldNotCount = strings.Contains(err.Error(), "this youtube channel is being managed by another server")
				if !shouldNotCount {
					SendInfoToSlack("A non fatal error was reported by the sync process. %s\nContinuing...", err.Error())
				}
			}
			SendInfoToSlack("Syncing %s (%s) reached an end. (iteration %d/%d - total processed channels: %d)", sync.LbryChannelName, sync.YoutubeChannelID, i+1, len(syncs), syncCount+1)
			if !shouldNotCount {
				syncCount++
			}
			if sync.IsInterrupted() || (s.Limit != 0 && syncCount >= s.Limit) {
				shouldInterruptLoop = true
				break
			}
		}
		if shouldInterruptLoop || s.SingleRun {
			break
		}
	}
	return nil
}

func (s *SyncManager) checkUsedSpace() error {
	usedPctile, err := GetUsedSpace(s.BlobsDir)
	if err != nil {
		return err
	}
	if usedPctile >= 0.90 && !s.SkipSpaceCheck {
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
