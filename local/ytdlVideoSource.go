package local

import (
	log "github.com/sirupsen/logrus"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"
)

type YtdlVideoSource struct {
	downloader Ytdl
	channelScanner YouTubeChannelScanner
	enrichers []YouTubeVideoEnricher
}

func NewYtdlVideoSource(downloadDir string, config *YouTubeSourceConfig, syncDB *SyncDb) (*YtdlVideoSource, error) {
	ytdl, err := NewYtdl(downloadDir)
	if err != nil {
		return nil, err
	}

	source := YtdlVideoSource {
		downloader: *ytdl,
	}

	if syncDB != nil {
		source.enrichers = append(source.enrichers, NewCacheVideoEnricher(syncDB))
	}

	if config.APIKey != "" {
		ytapiEnricher := NewYouTubeAPIVideoEnricher(config.APIKey)
		source.enrichers = append(source.enrichers, ytapiEnricher)
		source.channelScanner = NewYouTubeAPIChannelScanner(config.APIKey, config.ChannelID)
	}

	if source.channelScanner == nil {
		log.Warnf("No means of scanning source channels has been provided")
	}

	return &source, nil
}

func (s *YtdlVideoSource) SourceName() string {
	return "YouTube"
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
		Source: "YouTube",
		Title: &metadata.Title,
		Description: &metadata.Description,
		SourceURL: "\nhttps://www.youtube.com/watch?v=" + id,
		Languages: []string{},
		Tags: metadata.Tags,
		ReleaseTime: nil,
		ThumbnailURL: &bestThumbnail.URL,
		FullLocalPath: &videoPath,
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

func (s *YtdlVideoSource) Scan(sinceTimestamp int64) <-chan SourceScanIteratorResult {
	if s.channelScanner != nil {
		return s.channelScanner.Scan(sinceTimestamp)
	}

	videoCh := make(chan SourceScanIteratorResult, 1)
	close(videoCh)
	return videoCh
}
