package ytsync

import (
	"net/http"

	"github.com/lbryio/lbry.go/errors"

	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

func (s *Sync) CountVideos() (uint64, error) {
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.APIConfig.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return 0, errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Channels.List("statistics").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return 0, errors.Prefix("error getting channels", err)
	}

	if len(response.Items) < 1 {
		return 0, errors.Err("youtube channel not found")
	}

	return response.Items[0].Statistics.VideoCount, nil
}
