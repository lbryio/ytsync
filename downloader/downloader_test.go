package downloader

import (
	"testing"

	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestGetPlaylistVideoIDs(t *testing.T) {
	videoIDs, err := GetPlaylistVideoIDs("UCJ0-OtVpF0wOKEqT2Z1HEtA", 50, nil, nil)
	if err != nil {
		logrus.Error(err)
	}
	for _, id := range videoIDs {
		println(id)
	}
}

func TestGetVideoInformation(t *testing.T) {
	video, err := GetVideoInformation(nil, "zj7pXM9gE5M", nil, nil, nil)
	if err != nil {
		logrus.Error(err)
	}
	if video != nil {
		logrus.Info(video.ID)
	}
}

func Test_getUploadTime(t *testing.T) {
	configs := sdk.APIConfig{
		YoutubeAPIKey: "",
		ApiURL:        "https://api.lbry.com",
		ApiToken:      "Ht4NETrL5oWKyAaZkuSV68BKhtXkiLh5",
		HostName:      "test",
	}
	got, err := getUploadTime(&configs, "kDGOHNpRjzc", nil, "20060102")
	assert.NoError(t, err)
	t.Log(got)

}
