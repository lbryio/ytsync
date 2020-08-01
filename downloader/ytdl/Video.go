package ytdl

import (
	"time"
)

type YtdlVideo struct {
	UploadDate         string      `json:"upload_date"`
	UploadDateForReal  time.Time   // you need to manually set this since the value in the API doesn't include the time
	Extractor          string      `json:"extractor"`
	Series             interface{} `json:"series"`
	Format             string      `json:"format"`
	Vbr                interface{} `json:"vbr"`
	Chapters           interface{} `json:"chapters"`
	Height             int         `json:"height"`
	LikeCount          interface{} `json:"like_count"`
	Duration           int         `json:"duration"`
	Fulltitle          string      `json:"fulltitle"`
	PlaylistIndex      interface{} `json:"playlist_index"`
	Album              interface{} `json:"album"`
	ViewCount          int         `json:"view_count"`
	Playlist           interface{} `json:"playlist"`
	Title              string      `json:"title"`
	Filename           string      `json:"_filename"`
	Creator            interface{} `json:"creator"`
	Ext                string      `json:"ext"`
	ID                 string      `json:"id"`
	DislikeCount       interface{} `json:"dislike_count"`
	AverageRating      float64     `json:"average_rating"`
	Abr                int         `json:"abr"`
	UploaderURL        string      `json:"uploader_url"`
	Categories         []string    `json:"categories"`
	Fps                int         `json:"fps"`
	StretchedRatio     interface{} `json:"stretched_ratio"`
	SeasonNumber       interface{} `json:"season_number"`
	Annotations        interface{} `json:"annotations"`
	WebpageURLBasename string      `json:"webpage_url_basename"`
	Acodec             string      `json:"acodec"`
	DisplayID          string      `json:"display_id"`
	//RequestedFormats   []RequestedFormat `json:"requested_formats"`
	//AutomaticCaptions  struct{}          `json:"automatic_captions"`
	Description        string      `json:"description"`
	Tags               []string    `json:"tags"`
	Track              interface{} `json:"track"`
	RequestedSubtitles interface{} `json:"requested_subtitles"`
	StartTime          interface{} `json:"start_time"`
	Uploader           string      `json:"uploader"`
	ExtractorKey       string      `json:"extractor_key"`
	FormatID           string      `json:"format_id"`
	EpisodeNumber      interface{} `json:"episode_number"`
	UploaderID         string      `json:"uploader_id"`
	//Subtitles          struct{}          `json:"subtitles"`
	ReleaseYear interface{} `json:"release_year"`
	Thumbnails  []Thumbnail `json:"thumbnails"`
	License     interface{} `json:"license"`
	Artist      interface{} `json:"artist"`
	AgeLimit    int         `json:"age_limit"`
	ReleaseDate interface{} `json:"release_date"`
	AltTitle    interface{} `json:"alt_title"`
	Thumbnail   string      `json:"thumbnail"`
	ChannelID   string      `json:"channel_id"`
	IsLive      interface{} `json:"is_live"`
	Width       int         `json:"width"`
	EndTime     interface{} `json:"end_time"`
	WebpageURL  string      `json:"webpage_url"`
	//Formats            []Format          `json:"formats"`
	ChannelURL string      `json:"channel_url"`
	Resolution interface{} `json:"resolution"`
	Vcodec     string      `json:"vcodec"`
}

type RequestedFormat struct {
	Asr             interface{} `json:"asr"`
	Tbr             float64     `json:"tbr"`
	Container       string      `json:"container"`
	Language        interface{} `json:"language"`
	Format          string      `json:"format"`
	URL             string      `json:"url"`
	Vcodec          string      `json:"vcodec"`
	FormatNote      string      `json:"format_note"`
	Height          int         `json:"height"`
	Width           int         `json:"width"`
	Ext             string      `json:"ext"`
	FragmentBaseURL string      `json:"fragment_base_url"`
	Filesize        interface{} `json:"filesize"`
	Fps             int         `json:"fps"`
	ManifestURL     string      `json:"manifest_url"`
	Protocol        string      `json:"protocol"`
	FormatID        string      `json:"format_id"`
	HTTPHeaders     struct {
		AcceptCharset  string `json:"Accept-Charset"`
		AcceptLanguage string `json:"Accept-Language"`
		AcceptEncoding string `json:"Accept-Encoding"`
		Accept         string `json:"Accept"`
		UserAgent      string `json:"User-Agent"`
	} `json:"http_headers"`
	Fragments []struct {
		Path     string  `json:"path"`
		Duration float64 `json:"duration,omitempty"`
	} `json:"fragments"`
	Acodec string `json:"acodec"`
	Abr    int    `json:"abr,omitempty"`
}

type Format struct {
	Asr             int         `json:"asr"`
	Tbr             float64     `json:"tbr"`
	Protocol        string      `json:"protocol"`
	Format          string      `json:"format"`
	FormatNote      string      `json:"format_note"`
	Height          interface{} `json:"height"`
	ManifestURL     string      `json:"manifest_url,omitempty"`
	FormatID        string      `json:"format_id"`
	Container       string      `json:"container,omitempty"`
	Language        interface{} `json:"language,omitempty"`
	HTTPHeaders     HTTPHeaders `json:"http_headers"`
	URL             string      `json:"url"`
	Vcodec          string      `json:"vcodec"`
	Abr             int         `json:"abr,omitempty"`
	Width           interface{} `json:"width"`
	Ext             string      `json:"ext"`
	FragmentBaseURL string      `json:"fragment_base_url,omitempty"`
	Filesize        interface{} `json:"filesize"`
	Fps             float64     `json:"fps"`
	Fragments       []struct {
		Path     string  `json:"path"`
		Duration float64 `json:"duration,omitempty"`
	} `json:"fragments,omitempty"`
	Acodec    string      `json:"acodec"`
	PlayerURL interface{} `json:"player_url,omitempty"`
}

type Thumbnail struct {
	URL        string `json:"url"`
	Width      int    `json:"width"`
	Resolution string `json:"resolution"`
	ID         string `json:"id"`
	Height     int    `json:"height"`
}

type HTTPHeaders struct {
	AcceptCharset  string `json:"Accept-Charset"`
	AcceptLanguage string `json:"Accept-Language"`
	AcceptEncoding string `json:"Accept-Encoding"`
	Accept         string `json:"Accept"`
	UserAgent      string `json:"User-Agent"`
}
