package downloader

import (
	"testing"

	"github.com/lbryio/ytsync/v5/ip_manager"
	"github.com/lbryio/ytsync/v5/sdk"

	"github.com/lbryio/lbry.go/v2/extras/stop"

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
	s := stop.New()
	ip, err := ip_manager.GetIPPool(s)
	assert.NoError(t, err)
	video, err := GetVideoInformation("kDGOHNpRjzc", s.Ch(), ip)
	assert.NoError(t, err)
	assert.NotNil(t, video)
	logrus.Info(video.ID)
}

func Test_getUploadTime(t *testing.T) {
	configs := sdk.APIConfig{}
	got, err := getUploadTime(&configs, "kDGOHNpRjzc", nil, "20060102")
	assert.NoError(t, err)
	t.Log(got)
}
