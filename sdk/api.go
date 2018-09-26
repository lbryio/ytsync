package sdk

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/null"
)

const (
	MaxReasonLength = 500
)

type APIConfig struct {
	YoutubeAPIKey string
	ApiURL        string
	ApiToken      string
	HostName      string
}

type SyncProperties struct {
	SyncFrom         int64
	SyncUntil        int64
	YoutubeChannelID string
}

type YoutubeChannel struct {
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

func (a *APIConfig) FetchChannels(status string, cp *SyncProperties) ([]YoutubeChannel, error) {
	type apiJobsResponse struct {
		Success bool             `json:"success"`
		Error   null.String      `json:"error"`
		Data    []YoutubeChannel `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/jobs"
	res, _ := http.PostForm(endpoint, url.Values{
		"auth_token":  {a.ApiToken},
		"sync_status": {status},
		"min_videos":  {strconv.Itoa(1)},
		"after":       {strconv.Itoa(int(cp.SyncFrom))},
		"before":      {strconv.Itoa(int(cp.SyncUntil))},
		"sync_server": {a.HostName},
		"channel_id":  {cp.YoutubeChannelID},
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

type SyncedVideo struct {
	VideoID       string `json:"video_id"`
	Published     bool   `json:"published"`
	FailureReason string `json:"failure_reason"`
	ClaimName     string `json:"claim_name"`
}

func (a *APIConfig) SetChannelStatus(channelID string, status string, failureReason string) (map[string]SyncedVideo, map[string]bool, error) {
	type apiChannelStatusResponse struct {
		Success bool          `json:"success"`
		Error   null.String   `json:"error"`
		Data    []SyncedVideo `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/channel_status"
	if len(failureReason) > MaxReasonLength {
		failureReason = failureReason[:MaxReasonLength]
	}
	res, _ := http.PostForm(endpoint, url.Values{
		"channel_id":     {channelID},
		"sync_server":    {a.HostName},
		"auth_token":     {a.ApiToken},
		"sync_status":    {status},
		"failure_reason": {failureReason},
	})
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiChannelStatusResponse
	err := json.Unmarshal(body, &response)
	if err != nil {
		return nil, nil, err
	}
	if !response.Error.IsNull() {
		return nil, nil, errors.Err(response.Error.String)
	}
	if response.Data != nil {
		svs := make(map[string]SyncedVideo)
		claimNames := make(map[string]bool)
		for _, v := range response.Data {
			svs[v.VideoID] = v
			claimNames[v.ClaimName] = v.Published
		}
		return svs, claimNames, nil
	}
	return nil, nil, errors.Err("invalid API response. Status code: %d", res.StatusCode)
}

const (
	VideoStatusPublished = "published"
	VideoStatusFailed    = "failed"
)

func (a *APIConfig) MarkVideoStatus(channelID string, videoID string, status string, claimID string, claimName string, failureReason string, size *int64) error {
	endpoint := a.ApiURL + "/yt/video_status"
	if len(failureReason) > MaxReasonLength {
		failureReason = failureReason[:MaxReasonLength]
	}
	vals := url.Values{
		"youtube_channel_id": {channelID},
		"video_id":           {videoID},
		"status":             {status},
		"auth_token":         {a.ApiToken},
	}
	if status == VideoStatusPublished {
		if claimID == "" || claimName == "" {
			return errors.Err("claimID or claimName missing")
		}
		vals.Add("published_at", strconv.FormatInt(time.Now().Unix(), 10))
		vals.Add("claim_id", claimID)
		vals.Add("claim_name", claimName)
		if size != nil {
			vals.Add("size", strconv.FormatInt(*size, 10))
		}
	}
	if failureReason != "" {
		vals.Add("failure_reason", failureReason)
	}
	res, _ := http.PostForm(endpoint, vals)
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response struct {
		Success bool        `json:"success"`
		Error   null.String `json:"error"`
		Data    null.String `json:"data"`
	}
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
