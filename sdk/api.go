package sdk

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/null"

	log "github.com/sirupsen/logrus"
)

const (
	MaxReasonLength = 490
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

type Fee struct {
	Amount   string `json:"amount"`
	Address  string `json:"address"`
	Currency string `json:"currency"`
}
type YoutubeChannel struct {
	ChannelId          string `json:"channel_id"`
	TotalVideos        uint   `json:"total_videos"`
	DesiredChannelName string `json:"desired_channel_name"`
	Fee                *Fee   `json:"fee"`
	ChannelClaimID     string `json:"channel_claim_id"`
}

func (a *APIConfig) FetchChannels(status string, cp *SyncProperties) ([]YoutubeChannel, error) {
	type apiJobsResponse struct {
		Success bool             `json:"success"`
		Error   null.String      `json:"error"`
		Data    []YoutubeChannel `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/jobs"
	res, err := http.PostForm(endpoint, url.Values{
		"auth_token":  {a.ApiToken},
		"sync_status": {status},
		"min_videos":  {strconv.Itoa(1)},
		"after":       {strconv.Itoa(int(cp.SyncFrom))},
		"before":      {strconv.Itoa(int(cp.SyncUntil))},
		"sync_server": {a.HostName},
		"channel_id":  {cp.YoutubeChannelID},
	})
	if err != nil {
		return nil, errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiJobsResponse
	err = json.Unmarshal(body, &response)
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
	VideoID         string `json:"video_id"`
	Published       bool   `json:"published"`
	FailureReason   string `json:"failure_reason"`
	ClaimName       string `json:"claim_name"`
	ClaimID         string `json:"claim_id"`
	Size            int64  `json:"size"`
	MetadataVersion int8   `json:"metadata_version"`
}

func sanitizeFailureReason(s *string) {
	re := regexp.MustCompile("[[:^ascii:]]")
	*s = strings.Replace(re.ReplaceAllLiteralString(*s, ""), "\n", " ", -1)

	if len(*s) > MaxReasonLength {
		*s = (*s)[:MaxReasonLength]
	}
}
func (a *APIConfig) SetChannelStatus(channelID string, status string, failureReason string) (map[string]SyncedVideo, map[string]bool, error) {
	type apiChannelStatusResponse struct {
		Success bool          `json:"success"`
		Error   null.String   `json:"error"`
		Data    []SyncedVideo `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/channel_status"

	sanitizeFailureReason(&failureReason)
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
			if v.ClaimName != "" {
				claimNames[v.ClaimName] = v.Published
			}
		}
		return svs, claimNames, nil
	}
	return nil, nil, errors.Err("invalid API response. Status code: %d", res.StatusCode)
}

func (a *APIConfig) SetChannelClaimID(channelID string, channelClaimID string) error {
	type apiChannelStatusResponse struct {
		Success bool        `json:"success"`
		Error   null.String `json:"error"`
		Data    string      `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/set_channel_claim_id"
	res, _ := http.PostForm(endpoint, url.Values{
		"channel_id":       {channelID},
		"auth_token":       {a.ApiToken},
		"channel_claim_id": {channelClaimID},
	})
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	var response apiChannelStatusResponse
	err := json.Unmarshal(body, &response)
	if err != nil {
		return errors.Err(err)
	}
	if !response.Error.IsNull() {
		return errors.Err(response.Error.String)
	}
	if response.Data != "ok" {
		return errors.Err("Unexpected API response")
	}
	return nil
}

const (
	VideoStatusPublished     = "published"
	VideoStatusUpgradeFailed = "upgradefailed"
	VideoStatusFailed        = "failed"
)

func (a *APIConfig) DeleteVideos(videos []string) error {
	endpoint := a.ApiURL + "/yt/video_delete"
	videoIDs := strings.Join(videos, ",")
	vals := url.Values{
		"video_ids":  {videoIDs},
		"auth_token": {a.ApiToken},
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
		return errors.Err(err)
	}
	if !response.Error.IsNull() {
		return errors.Err(response.Error.String)
	}

	if !response.Data.IsNull() && response.Data.String == "ok" {
		return nil
	}
	return errors.Err("invalid API response. Status code: %d", res.StatusCode)
}

func (a *APIConfig) MarkVideoStatus(channelID string, videoID string, status string, claimID string, claimName string, failureReason string, size *int64, metadataVersion uint) error {
	endpoint := a.ApiURL + "/yt/video_status"

	sanitizeFailureReason(&failureReason)
	vals := url.Values{
		"youtube_channel_id": {channelID},
		"video_id":           {videoID},
		"status":             {status},
		"auth_token":         {a.ApiToken},
	}
	if status == VideoStatusPublished || status == VideoStatusUpgradeFailed {
		if claimID == "" || claimName == "" {
			return errors.Err("claimID (%s) or claimName (%s) missing", claimID, claimName)
		}
		vals.Add("published_at", strconv.FormatInt(time.Now().Unix(), 10))
		vals.Add("claim_id", claimID)
		vals.Add("claim_name", claimName)
		if metadataVersion > 0 {
			vals.Add("metadata_version", fmt.Sprintf("%d", metadataVersion))
		}
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
