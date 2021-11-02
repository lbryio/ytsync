package local

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/lbryio/lbry.go/v2/extras/util"
)

type YtdlVideoSource struct {
	downloader Ytdl
}

func NewYtdlVideoSource(downloadDir string) (*YtdlVideoSource, error) {
	ytdl, err := NewYtdl(downloadDir)
	if err != nil {
		return nil, err
	}

	source := YtdlVideoSource {
		downloader: *ytdl,
	}

	return &source, nil
}

func (s *YtdlVideoSource) GetVideo(id string) (*SourceVideo, error) {
	metadata, err := s.downloader.GetVideoMetadata(id)
	if err != nil {
		return nil, err
	}

	videoPath, err := s.downloader.GetVideoFile(id)
	if err != nil {
		return nil, err
	}

	sourceVideo := SourceVideo {
		ID: id,
		Title: &metadata.Title,
		Description: &metadata.Description,
		SourceURL: "\nhttps://www.youtube.com/watch?v=" + id,
		Languages: []string{},
		Tags: metadata.Tags,
		ReleaseTime: util.PtrToInt64(time.Now().Unix()),
		ThumbnailURL: nil,
		FullLocalPath: videoPath,
	}

	log.Debugf("Source video retrieved via ytdl: %v", sourceVideo)

	return &sourceVideo, nil
}
