package ytdl

import (
	"time"

	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/sirupsen/logrus"
)

type YtdlVideo struct {
	ID                string      `json:"id"`
	Title             string      `json:"title"`
	Thumbnails        []Thumbnail `json:"thumbnails"`
	Description       string      `json:"description"`
	ChannelID         string      `json:"channel_id"`
	Duration          int         `json:"duration"`
	Categories        []string    `json:"categories"`
	Tags              []string    `json:"tags"`
	IsLive            bool        `json:"is_live"`
	LiveStatus        string      `json:"live_status"`
	ReleaseTimestamp  int64       `json:"release_timestamp"`
	uploadDateForReal *time.Time
	Availability      string `json:"availability"`
	ReleaseDate       string `json:"release_date"`

	//WasLive           bool        `json:"was_live"`
	//Formats              interface{}   `json:"formats"`
	//Thumbnail            string        `json:"thumbnail"`
	//Uploader             string        `json:"uploader"`
	//UploaderID           string        `json:"uploader_id"`
	//UploaderURL          string        `json:"uploader_url"`
	//ChannelURL           string        `json:"channel_url"`
	//ViewCount            int           `json:"view_count"`
	//AverageRating        interface{}   `json:"average_rating"`
	//AgeLimit             int           `json:"age_limit"`
	//WebpageURL           string        `json:"webpage_url"`
	//PlayableInEmbed      bool          `json:"playable_in_embed"`
	//AutomaticCaptions    interface{}   `json:"automatic_captions"`
	//Subtitles            interface{}   `json:"subtitles"`
	//Chapters             interface{}   `json:"chapters"`
	//LikeCount            int           `json:"like_count"`
	//Channel              string        `json:"channel"`
	//ChannelFollowerCount int           `json:"channel_follower_count"`
	//UploadDate        string     `json:"upload_date"`
	//OriginalURL          string        `json:"original_url"`
	//WebpageURLBasename   string        `json:"webpage_url_basename"`
	//WebpageURLDomain     string        `json:"webpage_url_domain"`
	//Extractor            string        `json:"extractor"`
	//ExtractorKey         string        `json:"extractor_key"`
	//Playlist             interface{}   `json:"playlist"`
	//PlaylistIndex        interface{}   `json:"playlist_index"`
	//DisplayID            string        `json:"display_id"`
	//Fulltitle            string        `json:"fulltitle"`
	//DurationString       string        `json:"duration_string"`
	//RequestedSubtitles   interface{}   `json:"requested_subtitles"`
	//HasDrm               bool          `json:"__has_drm"`
	//RequestedFormats     interface{}   `json:"requested_formats"`
	//Format               string        `json:"format"`
	//FormatID             string        `json:"format_id"`
	//Ext                  string        `json:"ext"`
	//Protocol             string        `json:"protocol"`
	//Language             interface{}   `json:"language"`
	//FormatNote           string        `json:"format_note"`
	//FilesizeApprox       int           `json:"filesize_approx"`
	//Tbr                  float64       `json:"tbr"`
	//Width                int           `json:"width"`
	//Height               int           `json:"height"`
	//Resolution           string        `json:"resolution"`
	//Fps                  int           `json:"fps"`
	//DynamicRange         string        `json:"dynamic_range"`
	//Vcodec               string        `json:"vcodec"`
	//Vbr                  float64       `json:"vbr"`
	//StretchedRatio       interface{}   `json:"stretched_ratio"`
	//Acodec               string        `json:"acodec"`
	//Abr                  float64       `json:"abr"`
	//Asr                  int           `json:"asr"`
	//Epoch                int           `json:"epoch"`
	//Filename             string        `json:"filename"`
	//Urls                 string        `json:"urls"`
	//Type                 string        `json:"_type"`
}

type Thumbnail struct {
	URL        string `json:"url"`
	Preference int    `json:"preference"`
	ID         string `json:"id"`
	Height     int    `json:"height,omitempty"`
	Width      int    `json:"width,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

func (v *YtdlVideo) GetUploadTime() time.Time {
	//priority list:
	// release timestamp from yt
	// release timestamp from morty
	// release date from yt
	if v.uploadDateForReal != nil {
		return *v.uploadDateForReal
	}

	var ytdlReleaseTimestamp time.Time
	if v.ReleaseTimestamp > 0 {
		ytdlReleaseTimestamp = time.Unix(v.ReleaseTimestamp, 0).UTC()
	}
	//get morty timestamp
	var mortyReleaseTimestamp time.Time
	mortyRelease, err := sdk.GetAPIsConfigs().GetReleasedDate(v.ID)
	if err != nil {
		logrus.Error(err)
	} else if mortyRelease != nil {
		mortyReleaseTimestamp, err = time.ParseInLocation(time.RFC3339, mortyRelease.ReleaseTime, time.UTC)
		if err != nil {
			logrus.Error(err)
		}
	}

	ytdlReleaseDate, err := time.Parse("20060102", v.ReleaseDate)
	if err != nil {
		logrus.Error(err)
	}

	if !ytdlReleaseTimestamp.IsZero() {
		v.uploadDateForReal = &ytdlReleaseTimestamp
	} else if !mortyReleaseTimestamp.IsZero() {
		v.uploadDateForReal = &mortyReleaseTimestamp
	} else {
		v.uploadDateForReal = &ytdlReleaseDate
	}
	return *v.uploadDateForReal
}
