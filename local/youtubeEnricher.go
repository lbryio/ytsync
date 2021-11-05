package local

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/lbryio/lbry.go/v2/extras/util"
)

type YouTubeVideoEnricher interface {
	EnrichMissing(source *SourceVideo) error
}

type YouTubeAPIVideoEnricher struct {
	api *YouTubeAPI
}

func NewYouTubeAPIVideoEnricher(apiKey string) (*YouTubeAPIVideoEnricher) {
	enricher := YouTubeAPIVideoEnricher{
		api: NewYouTubeAPI(apiKey),
	}
	return &enricher
}

func (e *YouTubeAPIVideoEnricher) EnrichMissing(source *SourceVideo) error {
	if source.ReleaseTime != nil {
		log.Debugf("Video %s does not need enrichment. YouTubeAPIVideoEnricher is skipping.", source.ID)
		return nil
	}

	snippet, err := e.api.GetVideoSnippet(source.ID)
	if err != nil {
		log.Errorf("Error snippet data for video %s: %v", err)
		return err
	}

	publishedAt, err := time.Parse(time.RFC3339, snippet.PublishedAt)
	if err != nil {
		log.Errorf("Error converting publishedAt to timestamp: %v", err)
	} else {
		source.ReleaseTime = util.PtrToInt64(publishedAt.Unix())
	}
	return nil
}