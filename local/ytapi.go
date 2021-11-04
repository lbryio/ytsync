package local

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
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

type videoListResponse struct {
	Items []struct {
		Snippet VideoSnippet `json:"snippet"`
	} `json:"items"`
}

type VideoSnippet struct {
	PublishedAt string `json:"publishedAt"`
}
