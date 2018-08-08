package ytsync

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
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
	YoutubeAPIKey           string
	ApiURL                  string
	ApiToken                string
	BlobsDir                string
	VideosLimit             int
	MaxVideoSize            int
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
}

func (s SyncManager) fetchChannels(status string) ([]apiYoutubeChannel, error) {
	endpoint := s.ApiURL + "/yt/jobs"
	res, _ := http.PostForm(endpoint, url.Values{
		"auth_token":  {s.ApiToken},
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

type apiChannelStatusResponse struct {
	Success bool          `json:"success"`
	Error   null.String   `json:"error"`
	Data    []syncedVideo `json:"data"`
}

type syncedVideo struct {
	VideoID       string `json:"video_id"`
	Published     bool   `json:"published"`
	FailureReason string `json:"failure_reason"`
}

func (s SyncManager) setChannelStatus(channelID string, status string) (map[string]syncedVideo, error) {
	endpoint := s.ApiURL + "/yt/channel_status"

	res, _ := http.PostForm(endpoint, url.Values{
		"channel_id":  {channelID},
		"sync_server": {s.HostName},
		"auth_token":  {s.ApiToken},
		"sync_status": {status},
	})
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiChannelStatusResponse
	err := json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}
	if !response.Error.IsNull() {
		return nil, errors.Err(response.Error.String)
	}
	if response.Data != nil {
		svs := make(map[string]syncedVideo)
		for _, v := range response.Data {
			svs[v.VideoID] = v
		}
		return svs, nil
	}
	return nil, errors.Err("invalid API response. Status code: %d", res.StatusCode)
}

const (
	VideoStatusPublished = "published"
	VideoStatusFailed    = "failed"
)

type apiVideoStatusResponse struct {
	Success bool        `json:"success"`
	Error   null.String `json:"error"`
	Data    null.String `json:"data"`
}

func (s SyncManager) MarkVideoStatus(channelID string, videoID string, status string, claimID string, claimName string, failureReason string) error {
	endpoint := s.ApiURL + "/yt/video_status"

	vals := url.Values{
		"youtube_channel_id": {channelID},
		"video_id":           {videoID},
		"status":             {status},
		"auth_token":         {s.ApiToken},
	}
	if status == VideoStatusPublished {
		if claimID == "" || claimName == "" {
			return errors.Err("claimID or claimName missing")
		}
		vals.Add("published_at", strconv.FormatInt(time.Now().Unix(), 10))
		vals.Add("claim_id", claimID)
		vals.Add("claim_name", claimName)
	}
	if failureReason != "" {
		vals.Add("failure_reason", failureReason)
	}
	res, _ := http.PostForm(endpoint, vals)
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiVideoStatusResponse
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
	return errors.Err("invalid API response. Status code: %d", res.StatusCode)
}

func (s SyncManager) Start() error {
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
				YoutubeAPIKey:           s.YoutubeAPIKey,
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
						YoutubeAPIKey:           s.YoutubeAPIKey,
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
			SendInfoToSlack("Syncing %s (%s) to LBRY! (iteration %d/%d - total session iterations: %d)", sync.LbryChannelName, sync.YoutubeChannelID, i+1, len(syncs), syncCount+1)
			err := sync.FullCycle()
			if err != nil {
				fatalErrors := []string{
					"default_wallet already exists",
					"WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR",
					"NotEnoughFunds",
					"no space left on device",
				}
				if util.SubstringInSlice(err.Error(), fatalErrors) {
					return errors.Prefix("@Nikooo777 this requires manual intervention! Exiting...", err)
				}
				SendInfoToSlack("A non fatal error was reported by the sync process. %s\nContinuing...", err.Error())
			}
			SendInfoToSlack("Syncing %s (%s) reached an end. (Iteration %d/%d - total session iterations: %d))", sync.LbryChannelName, sync.YoutubeChannelID, i+1, len(syncs), syncCount+1)
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
	usedPctile, err := GetUsedSpace(s.BlobsDir)
	if err != nil {
		return err
	}
	if usedPctile >= 0.90 && !s.SkipSpaceCheck {
		return errors.Err(fmt.Sprintf("more than 90%% of the space has been used. use --skip-space-check to ignore. Used: %.1f%%", usedPctile*100))
	}
	SendInfoToSlack("disk usage: %.1f%%", usedPctile*100)
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
