package ytsync

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strconv"

	"time"

	"syscall"

	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/null"
	"github.com/lbryio/lbry.go/util"
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
	SyncStatus              string
	SyncFrom                int64
	SyncUntil               int64
	ConcurrentJobs          int
	ConcurrentVideos        int
	HostName                string
	YoutubeChannelID        string

	youtubeAPIKey string
	apiURL        string
	apiToken      string
}

const (
	StatusPending = "pending" // waiting for permission to sync
	StatusQueued  = "queued"  // in sync queue. will be synced soon
	StatusSyncing = "syncing" // syncing now
	StatusSynced  = "synced"  // done
	StatusFailed  = "failed"
)

var SyncStatuses = []string{StatusPending, StatusQueued, StatusSyncing, StatusSynced, StatusFailed}

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
}

func (s SyncManager) fetchChannels(status string) ([]apiYoutubeChannel, error) {
	endpoint := s.apiURL + "/yt/jobs"
	res, _ := http.PostForm(endpoint, url.Values{
		"auth_token":  {s.apiToken},
		"sync_status": {status},
		"min_videos":  {strconv.Itoa(1)},
		"after":       {strconv.Itoa(int(s.SyncFrom))},
		"before":      {strconv.Itoa(int(s.SyncUntil))},
		//"sync_server": {s.HostName},
		"channel_id": {s.YoutubeChannelID},
	})
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiJobsResponse
	err := json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, errors.Err(response.Error)
	}
	log.Printf("Fetched channels: %d", len(response.Data))
	return response.Data, nil
}

type apiSyncUpdateResponse struct {
	Success bool        `json:"success"`
	Error   null.String `json:"error"`
	Data    null.String `json:"data"`
}

func (s SyncManager) setChannelSyncStatus(channelID string, status string) error {
	endpoint := s.apiURL + "/yt/sync_update"

	res, _ := http.PostForm(endpoint, url.Values{
		"channel_id":  {channelID},
		"sync_server": {s.HostName},
		"auth_token":  {s.apiToken},
		"sync_status": {status},
	})
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiSyncUpdateResponse
	err := json.Unmarshal(body, &response)
	if err != nil {
		return err
	}
	if !response.Error.IsNull() {
		return errors.Err(response.Error.String)
	}
	if !response.Data.IsNull() && response.Data.String == "ok" {
		return nil
	}
	return errors.Err("invalid API response")
}

func (s SyncManager) Start() error {
	s.apiURL = os.Getenv("LBRY_API")
	s.apiToken = os.Getenv("LBRY_API_TOKEN")
	s.youtubeAPIKey = os.Getenv("YOUTUBE_API_KEY")
	if s.apiURL == "" {
		return errors.Err("An API URL was not defined. Please set the environment variable LBRY_API")
	}
	if s.apiToken == "" {
		return errors.Err("An API Token was not defined. Please set the environment variable LBRY_API_TOKEN")
	}
	if s.youtubeAPIKey == "" {
		return errors.Err("A Youtube API key was not defined. Please set the environment variable YOUTUBE_API_KEY")
	}

	syncCount := 0
	for {
		err := s.checkUsedSpace()
		if err != nil {
			return err
		}

		var syncs []Sync
		shouldInterruptLoop := false

		isSingleChannelSync := s.YoutubeChannelID != ""
		if isSingleChannelSync {
			channels, err := s.fetchChannels("")
			if err != nil {
				return err
			}
			if len(channels) != 1 {
				return errors.Err("Expected 1 channel, %d returned", len(channels))
			}
			lbryChannelName := channels[0].DesiredChannelName
			if !s.isWorthProcessing(channels[0]) {
				break
			}
			syncs = make([]Sync, 1)
			syncs[0] = Sync{
				YoutubeAPIKey:           s.youtubeAPIKey,
				YoutubeChannelID:        s.YoutubeChannelID,
				LbryChannelName:         lbryChannelName,
				StopOnError:             s.StopOnError,
				MaxTries:                s.MaxTries,
				ConcurrentVideos:        s.ConcurrentVideos,
				TakeOverExistingChannel: s.TakeOverExistingChannel,
				Refill:                  s.Refill,
				Manager:                 &s,
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
				channels, err := s.fetchChannels(q)
				if err != nil {
					return err
				}
				for _, c := range channels {
					if !s.isWorthProcessing(c) {
						continue
					}
					syncs = append(syncs, Sync{
						YoutubeAPIKey:           s.youtubeAPIKey,
						YoutubeChannelID:        c.ChannelId,
						LbryChannelName:         c.DesiredChannelName,
						StopOnError:             s.StopOnError,
						MaxTries:                s.MaxTries,
						ConcurrentVideos:        s.ConcurrentVideos,
						TakeOverExistingChannel: s.TakeOverExistingChannel,
						Refill:                  s.Refill,
						Manager:                 &s,
					})
				}
			}
		}
		if len(syncs) == 0 {
			log.Infoln("No channels to sync. Pausing 5 minutes!")
			time.Sleep(5 * time.Minute)
		}
		for i, sync := range syncs {
			util.SendInfoToSlack("Syncing %s to LBRY! (iteration %d/%d - total session iterations: %d)", sync.LbryChannelName, i, len(syncs), syncCount)
			err := sync.FullCycle()
			if err != nil {
				fatalErrors := []string{
					"default_wallet already exists",
					"WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR",
					"NotEnoughFunds",
					"no space left on device",
				}
				if util.ContainedInSlice(err.Error(), fatalErrors) {
					return errors.Prefix("@Nikooo777 this requires manual intervention! Exiting...", err)
				}
				util.SendInfoToSlack("A non fatal error was reported by the sync process. %s\nContinuing...", err.Error())
			}
			util.SendInfoToSlack("Syncing %s reached an end. (Iteration %d/%d - total session iterations: %d))", sync.LbryChannelName, i, len(syncs), syncCount)
			syncCount++
			if sync.IsInterrupted() || (s.Limit != 0 && syncCount >= s.Limit) {
				shouldInterruptLoop = true
				break
			}

		}
		if shouldInterruptLoop {
			break
		}
	}
	return nil
}

func (s SyncManager) isWorthProcessing(channel apiYoutubeChannel) bool {
	return channel.TotalVideos > 0 && (channel.SyncServer.IsNull() || channel.SyncServer.String == s.HostName)
}

func (s SyncManager) checkUsedSpace() error {
	usr, err := user.Current()
	if err != nil {
		return err
	}
	usedPctile, err := GetUsedSpace(usr.HomeDir + "/.lbrynet/blobfiles/")
	if err != nil {
		return err
	}
	if usedPctile >= 0.90 && !s.SkipSpaceCheck {
		return errors.Err(fmt.Sprintf("more than 90%% of the space has been used. use --skip-space-check to ignore. Used: %.1f%%", usedPctile*100))
	}
	util.SendInfoToSlack("disk usage: %.1f%%", usedPctile*100)
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
