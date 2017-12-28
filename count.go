package ytsync

import (
	"net/http"

	"github.com/go-errors/errors"
	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

func (s *Sync) CountVideos() (uint64, error) {
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return 0, errors.WrapPrefix(err, "error creating YouTube service", 0)
	}

	response, err := service.Channels.List("statistics").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return 0, errors.WrapPrefix(err, "error getting channels", 0)
	}

	if len(response.Items) < 1 {
		return 0, errors.New("youtube channel not found")
	}

	return response.Items[0].Statistics.VideoCount, nil
}
