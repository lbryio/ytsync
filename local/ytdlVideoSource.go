package local

import (
	log "github.com/sirupsen/logrus"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"
)

type YtdlVideoSource struct {
	downloader Ytdl
	enrichers []YouTubeVideoEnricher
}

func NewYtdlVideoSource(downloadDir string, config *YouTubeSourceConfig) (*YtdlVideoSource, error) {
	ytdl, err := NewYtdl(downloadDir)
	if err != nil {
		return nil, err
	}

	source := YtdlVideoSource {
		downloader: *ytdl,
	}

	if config.YouTubeAPIKey != "" {
		ytapiEnricher := NewYouTubeAPIVideoEnricher(config.YouTubeAPIKey)
		source.enrichers = append(source.enrichers, ytapiEnricher)
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

	var bestThumbnail *ytdl.Thumbnail = nil
	for i, thumbnail := range metadata.Thumbnails {
		if i == 0 || bestThumbnail.Width < thumbnail.Width {
			bestThumbnail = &thumbnail
		}
	}

	sourceVideo := SourceVideo {
		ID: id,
		Title: &metadata.Title,
		Description: &metadata.Description,
		SourceURL: "\nhttps://www.youtube.com/watch?v=" + id,
		Languages: []string{},
		Tags: metadata.Tags,
		ReleaseTime: nil,
		ThumbnailURL: &bestThumbnail.URL,
		FullLocalPath: videoPath,
	}

	for _, enricher := range s.enrichers {
		err = enricher.EnrichMissing(&sourceVideo)
		if err != nil {
			log.Warnf("Error enriching video %s, continuing enrichment: %v", id, err)
		}
	}

	log.Debugf("Source video retrieved via ytdl: %v", sourceVideo)

	return &sourceVideo, nil
}

func (s *YtdlVideoSource) DeleteLocalCache(id string) error {
	return s.downloader.DeleteVideoFiles(id)
}
