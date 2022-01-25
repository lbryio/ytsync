package local

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/lbryio/lbry.go/v2/extras/util"
)

type YouTubeAPI struct {
	apiKey string
	client *http.Client
}

func NewYouTubeAPI(apiKey string) (*YouTubeAPI) {
	client := &http.Client {
		Transport: &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: true,
		},
	}

	api := YouTubeAPI {
		apiKey: apiKey,
		client: client,
	}

	return &api
}

func (a *YouTubeAPI) GetVideoSnippet(videoID string) (*VideoSnippet, error) {
	req, err := http.NewRequest("GET", "https://youtube.googleapis.com/youtube/v3/videos", nil)
	if err != nil {
		log.Errorf("Error creating http client for YouTube API: %v", err)
		return nil, err
	}

	query := req.URL.Query()
	query.Add("part", "snippet")
	query.Add("id", videoID)
	query.Add("key", a.apiKey)
	req.URL.RawQuery = query.Encode()

	req.Header.Add("Accept", "application/json")

	resp, err := a.client.Do(req)
	defer resp.Body.Close()
	if err != nil {
		log.Errorf("Error from YouTube API: %v", err)
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	log.Tracef("Response from YouTube API: %s", string(body[:]))

	var result videoListResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Errorf("Error deserializing video list response from YouTube API: %v", err)
		return nil, err
	}

	if len(result.Items) != 1 {
		err = fmt.Errorf("YouTube API responded with incorrect number of snippets (%d) while attempting to get snippet data for video %s", len(result.Items), videoID)
		return nil, err
	}

	return &result.Items[0].Snippet, nil
}

func (a *YouTubeAPI) GetChannelVideosPage(channelID string, publishedAfter int64, pageToken string) ([]SourceVideo, string, error) {
	req, err := http.NewRequest("GET", "https://youtube.googleapis.com/youtube/v3/search", nil)
	if err != nil {
		log.Errorf("Error creating http client for YouTube API: %v", err)
		return []SourceVideo{}, "", err
	}

	query := req.URL.Query()
	query.Add("part", "snippet")
	query.Add("type", "video")
	query.Add("channelId", channelID)
	query.Add("publishedAfter", time.Unix(publishedAfter, 0).Format(time.RFC3339))
	query.Add("maxResults", "5")
	if pageToken != "" {
		query.Add("pageToken", pageToken)
	}
	query.Add("key", a.apiKey)
	req.URL.RawQuery = query.Encode()

	req.Header.Add("Accept", "application/json")

	resp, err := a.client.Do(req)
	defer resp.Body.Close()
	if err != nil {
		log.Errorf("Error from YouTube API: %v", err)
		return []SourceVideo{}, "", err
	}

	body, err := io.ReadAll(resp.Body)
	log.Tracef("Response from YouTube API: %s", string(body[:]))

	var result videoSearchResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Errorf("Error deserializing video list response from YouTube API: %v", err)
		return []SourceVideo{}, "", err
	}

	videos := []SourceVideo{}
	for _, item := range result.Items {
		var releaseTime *int64
		publishedAt, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
		if err != nil {
			log.Errorf("Unable to parse publish time of %s while scanning YouTube channel %s: %v", item.ID.VideoID, channelID)
			releaseTime = nil
		} else {
			releaseTime = util.PtrToInt64(publishedAt.Unix())
		}

		video := SourceVideo {
			ID: item.ID.VideoID,
			Source: "YouTube",
			ReleaseTime: releaseTime,
		}
		videos = append(videos, video)
	}

	return videos, result.NextPageToken, nil
}

type videoListResponse struct {
	NextPageToken string `json:"nextPageToken"`
	Items []struct {
		ID string `json:"id"`
		Snippet VideoSnippet `json:"snippet"`
	} `json:"items"`
}

type videoSearchResponse struct {
	NextPageToken string `json:"nextPageToken"`
	Items []struct {
		ID struct{
			VideoID string `json:"videoId"`
		} `json:"id"`
		Snippet VideoSnippet `json:"snippet"`
	} `json:"items"`
}

type VideoSnippet struct {
	PublishedAt string `json:"publishedAt"`
}
