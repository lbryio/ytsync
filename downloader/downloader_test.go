package downloader

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestRun(t *testing.T) {
	//args := []string{"--skip-download", "https://www.youtube.com/c/Electroboom", "--get-id", "--flat-playlist"}
	//videoIDs, err := GetPlaylistVideoIDs("Electroboom")
	videoIDs, err := GetPlaylistVideoIDs("UCJ0-OtVpF0wOKEqT2Z1HEtA")
	if err != nil {
		logrus.Error(err)
	}
	for _, id := range videoIDs {
		println(id)
	}
	t.Error("just stop")
}
