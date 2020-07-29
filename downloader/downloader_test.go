package downloader

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestGetPlaylistVideoIDs(t *testing.T) {
	videoIDs, err := GetPlaylistVideoIDs("UCJ0-OtVpF0wOKEqT2Z1HEtA", 50)
	if err != nil {
		logrus.Error(err)
	}
	for _, id := range videoIDs {
		println(id)
	}
}

func TestGetVideoInformation(t *testing.T) {
	video, err := GetVideoInformation("zj7pXM9gE5M", nil)
	if err != nil {
		logrus.Error(err)
	}
	if video != nil {
		logrus.Info(video.ID)
	}
}
