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

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/null"
	"github.com/lbryio/ytsync/v5/shared"

	"github.com/lbryio/ytsync/v5/util"

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

func (a *APIConfig) FetchChannels(status string, cliFlags *shared.SyncFlags) ([]shared.YoutubeChannel, error) {
	type apiJobsResponse struct {
		Success bool                    `json:"success"`
		Error   null.String             `json:"error"`
		Data    []shared.YoutubeChannel `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/jobs"
	res, err := http.PostForm(endpoint, url.Values{
		"auth_token":  {a.ApiToken},
		"sync_status": {status},
		"min_videos":  {strconv.Itoa(1)},
		"after":       {strconv.Itoa(int(cliFlags.SyncFrom))},
		"before":      {strconv.Itoa(int(cliFlags.SyncUntil))},
		"sync_server": {a.HostName},
		"channel_id":  {cliFlags.ChannelID},
	})
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.FetchChannels(status, cliFlags)
		}
		return nil, errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.FetchChannels(status, cliFlags)
	}
	var response apiJobsResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, errors.Err(err)
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
	Transferred     bool   `json:"transferred"`
	IsLbryFirst     bool   `json:"is_lbry_first"`
}

func sanitizeFailureReason(s *string) {
	re := regexp.MustCompile("[[:^ascii:]]")
	*s = strings.Replace(re.ReplaceAllLiteralString(*s, ""), "\n", " ", -1)

	if len(*s) > MaxReasonLength {
		*s = (*s)[:MaxReasonLength]
	}
}

func (a *APIConfig) SetChannelCert(certHex string, channelID string) error {
	type apiSetChannelCertResponse struct {
		Success bool        `json:"success"`
		Error   null.String `json:"error"`
		Data    string      `json:"data"`
	}

	endpoint := a.ApiURL + "/yt/channel_cert"

	res, err := http.PostForm(endpoint, url.Values{
		"channel_claim_id": {channelID},
		"channel_cert":     {certHex},
		"auth_token":       {a.ApiToken},
	})
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.SetChannelCert(certHex, channelID)
		}
		return errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.SetChannelCert(certHex, channelID)
	}
	var response apiSetChannelCertResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return errors.Err(err)
	}
	if !response.Error.IsNull() {
		return errors.Err(response.Error.String)
	}

	return nil
}

func (a *APIConfig) SetChannelStatus(channelID string, status string, failureReason string, transferState *int) (map[string]SyncedVideo, map[string]bool, error) {
	type apiChannelStatusResponse struct {
		Success bool          `json:"success"`
		Error   null.String   `json:"error"`
		Data    []SyncedVideo `json:"data"`
	}
	endpoint := a.ApiURL + "/yt/channel_status"

	sanitizeFailureReason(&failureReason)
	params := url.Values{
		"channel_id":     {channelID},
		"sync_server":    {a.HostName},
		"auth_token":     {a.ApiToken},
		"sync_status":    {status},
		"failure_reason": {failureReason},
	}
	if transferState != nil {
		params.Add("transfer_state", strconv.Itoa(*transferState))
	}
	res, err := http.PostForm(endpoint, params)
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.SetChannelStatus(channelID, status, failureReason, transferState)
		}
		return nil, nil, errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode >= http.StatusInternalServerError {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.SetChannelStatus(channelID, status, failureReason, transferState)
	}
	var response apiChannelStatusResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, nil, errors.Err(err)
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
	res, err := http.PostForm(endpoint, url.Values{
		"channel_id":       {channelID},
		"auth_token":       {a.ApiToken},
		"channel_claim_id": {channelClaimID},
	})
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.SetChannelClaimID(channelID, channelClaimID)
		}
		return errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.SetChannelClaimID(channelID, channelClaimID)
	}
	var response apiChannelStatusResponse
	err = json.Unmarshal(body, &response)
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
	res, err := http.PostForm(endpoint, vals)
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.DeleteVideos(videos)
		}
		return errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.DeleteVideos(videos)
	}
	var response struct {
		Success bool        `json:"success"`
		Error   null.String `json:"error"`
		Data    null.String `json:"data"`
	}
	err = json.Unmarshal(body, &response)
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

func (a *APIConfig) MarkVideoStatus(status shared.VideoStatus) error {
	endpoint := a.ApiURL + "/yt/video_status"

	sanitizeFailureReason(&status.FailureReason)
	vals := url.Values{
		"youtube_channel_id": {status.ChannelID},
		"video_id":           {status.VideoID},
		"status":             {status.Status},
		"auth_token":         {a.ApiToken},
	}
	if status.Status == VideoStatusPublished || status.Status == VideoStatusUpgradeFailed {
		if status.ClaimID == "" || status.ClaimName == "" {
			return errors.Err("claimID (%s) or claimName (%s) missing", status.ClaimID, status.ClaimName)
		}
		vals.Add("published_at", strconv.FormatInt(time.Now().Unix(), 10))
		vals.Add("claim_id", status.ClaimID)
		vals.Add("claim_name", status.ClaimName)
		if status.MetaDataVersion > 0 {
			vals.Add("metadata_version", fmt.Sprintf("%d", status.MetaDataVersion))
		}
		if status.Size != nil {
			vals.Add("size", strconv.FormatInt(*status.Size, 10))
		}
	}
	if status.FailureReason != "" {
		vals.Add("failure_reason", status.FailureReason)
	}
	if status.IsTransferred != nil {
		vals.Add("transferred", strconv.FormatBool(*status.IsTransferred))
	}
	res, err := http.PostForm(endpoint, vals)
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s for %s. Waiting to retry", endpoint, status.ClaimName)
			time.Sleep(30 * time.Second)
			return a.MarkVideoStatus(status)
		}
		return errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.MarkVideoStatus(status)
	}
	var response struct {
		Success bool        `json:"success"`
		Error   null.String `json:"error"`
		Data    null.String `json:"data"`
	}
	err = json.Unmarshal(body, &response)
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

func (a *APIConfig) VideoState(videoID string) (string, error) {
	endpoint := a.ApiURL + "/yt/video_state"
	vals := url.Values{
		"video_id":   {videoID},
		"auth_token": {a.ApiToken},
	}

	res, err := http.PostForm(endpoint, vals)
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.VideoState(videoID)
		}
		return "", errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return "not_found", nil
	}
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.VideoState(videoID)
	}
	var response struct {
		Success bool        `json:"success"`
		Error   null.String `json:"error"`
		Data    null.String `json:"data"`
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return "", errors.Err(err)
	}
	if !response.Error.IsNull() {
		return "", errors.Err(response.Error.String)
	}
	if !response.Data.IsNull() {
		return response.Data.String, nil
	}
	return "", errors.Err("invalid API response. Status code: %d", res.StatusCode)
}

type VideoRelease struct {
	ID            uint64 `json:"id"`
	YoutubeDataID uint64 `json:"youtube_data_id"`
	VideoID       string `json:"video_id"`
	ReleaseTime   string `json:"release_time"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func (a *APIConfig) GetReleasedDate(videoID string) (*VideoRelease, error) {
	endpoint := a.ApiURL + "/yt/released"
	vals := url.Values{
		"video_id":   {videoID},
		"auth_token": {a.ApiToken},
	}

	res, err := http.PostForm(endpoint, vals)
	if err != nil {
		if strings.Contains(err.Error(), "EOF") {
			util.SendErrorToSlack("EOF error while trying to call %s. Waiting to retry", endpoint)
			time.Sleep(30 * time.Second)
			return a.GetReleasedDate(videoID)
		}
		return nil, errors.Err(err)
	}
	defer res.Body.Close()
	body, _ := ioutil.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		util.SendErrorToSlack("Error %d while trying to call %s. Waiting to retry", res.StatusCode, endpoint)
		log.Debugln(string(body))
		time.Sleep(30 * time.Second)
		return a.GetReleasedDate(videoID)
	}
	var response struct {
		Success bool         `json:"success"`
		Error   null.String  `json:"error"`
		Data    VideoRelease `json:"data"`
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, errors.Err(err)
	}
	if !response.Error.IsNull() {
		return nil, errors.Err(response.Error.String)
	}
	if response.Data.ReleaseTime != "" {
		return &response.Data, nil
	}
	return nil, errors.Err("invalid API response. Status code: %d", res.StatusCode)
}
