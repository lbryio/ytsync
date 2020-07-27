package manager

import (
	"github.com/lbryio/ytsync/v5/ytapi"
)

func (s *Sync) CountVideos() (uint64, error) {
	return ytapi.VideosInChannel(s.APIConfig.YoutubeAPIKey, s.YoutubeChannelID)
}
