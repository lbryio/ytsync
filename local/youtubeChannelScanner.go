package local

type YouTubeChannelScanner interface {
	Scan(sinceTimestamp int64) <-chan SourceScanIteratorResult
}

type YouTubeAPIChannelScanner struct {
	api *YouTubeAPI
	channel string
}

func NewYouTubeAPIChannelScanner(apiKey, channel string) (*YouTubeAPIChannelScanner) {
	scanner := YouTubeAPIChannelScanner {
		api: NewYouTubeAPI(apiKey),
		channel: channel,
	}
	return &scanner
}

func (s *YouTubeAPIChannelScanner) Scan(sinceTimestamp int64) <-chan SourceScanIteratorResult {
	videoCh := make(chan SourceScanIteratorResult, 10)
	go func() {
		defer close(videoCh)

		for firstRun, nextPage := true, ""; firstRun || nextPage != ""; {
			var videos []SourceVideo
			var err error

			firstRun = false

			videos, nextPage, err = s.api.GetChannelVideosPage(s.channel, sinceTimestamp, nextPage)
			if err != nil {
				videoCh <- SourceScanIteratorResult {
					Video: nil,
					Error: err,
				}
				return
			}

			for _, video := range videos {
				outVideo := video
				videoCh <- SourceScanIteratorResult {
					Video: &outVideo,
					Error: nil,
				}
			}
		}
	}()

	return videoCh
}
