package ytdl

import "time"

type YtdlVideo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Formats []struct {
		FormatId   string `json:"format_id"`
		FormatNote string `json:"format_note"`
		Ext        string `json:"ext"`
		Protocol   string `json:"protocol"`
		Acodec     string `json:"acodec"`
		Vcodec     string `json:"vcodec"`
		Url        string `json:"url"`
		Width      int    `json:"width,omitempty"`
		Height     int    `json:"height,omitempty"`
		Fragments  []struct {
			Path     string  `json:"path"`
			Duration float64 `json:"duration"`
		} `json:"fragments,omitempty"`
		AudioExt    string `json:"audio_ext"`
		VideoExt    string `json:"video_ext"`
		Format      string `json:"format"`
		Resolution  string `json:"resolution"`
		HttpHeaders struct {
			UserAgent      string `json:"User-Agent"`
			Accept         string `json:"Accept"`
			AcceptEncoding string `json:"Accept-Encoding"`
			AcceptLanguage string `json:"Accept-Language"`
		} `json:"http_headers"`
		Asr                float64 `json:"asr,omitempty"`
		Filesize           int64   `json:"filesize,omitempty"`
		SourcePreference   int     `json:"source_preference,omitempty"`
		Quality            int     `json:"quality,omitempty"`
		Tbr                float64 `json:"tbr,omitempty"`
		Language           string  `json:"language,omitempty"`
		LanguagePreference int     `json:"language_preference,omitempty"`
		Abr                float64 `json:"abr,omitempty"`
		DownloaderOptions  struct {
			HttpChunkSize int `json:"http_chunk_size"`
		} `json:"downloader_options,omitempty"`
		Container      string  `json:"container,omitempty"`
		Fps            float64 `json:"fps,omitempty"`
		DynamicRange   string  `json:"dynamic_range,omitempty"`
		Vbr            float64 `json:"vbr,omitempty"`
		FilesizeApprox float64 `json:"filesize_approx,omitempty"`
	} `json:"formats"`
	Thumbnails         []Thumbnail `json:"thumbnails"`
	Thumbnail          string      `json:"thumbnail"`
	Description        string      `json:"description"`
	UploadDate         string      `json:"upload_date"`
	UploadDateForReal  time.Time   `json:"upload_date_for_real"`
	Uploader           string      `json:"uploader"`
	UploaderId         string      `json:"uploader_id"`
	UploaderUrl        string      `json:"uploader_url"`
	ChannelID          string      `json:"channel_id"`
	ChannelUrl         string      `json:"channel_url"`
	Duration           int         `json:"duration"`
	ViewCount          int         `json:"view_count"`
	AgeLimit           int         `json:"age_limit"`
	WebpageUrl         string      `json:"webpage_url"`
	Categories         []string    `json:"categories"`
	Tags               []string    `json:"tags"`
	PlayableInEmbed    bool        `json:"playable_in_embed"`
	IsLive             bool        `json:"is_live"`
	WasLive            bool        `json:"was_live"`
	LiveStatus         string      `json:"live_status"`
	ReleaseTimestamp   int64       `json:"release_timestamp"`
	LikeCount          int         `json:"like_count"`
	Channel            string      `json:"channel"`
	Availability       string      `json:"availability"`
	WebpageUrlBasename string      `json:"webpage_url_basename"`
	WebpageUrlDomain   string      `json:"webpage_url_domain"`
	Extractor          string      `json:"extractor"`
	ExtractorKey       string      `json:"extractor_key"`
	DisplayId          string      `json:"display_id"`
	DurationString     string      `json:"duration_string"`
	ReleaseDate        string      `json:"release_date"`
	Asr                float64     `json:"asr"`
	FormatId           string      `json:"format_id"`
	FormatNote         string      `json:"format_note"`
	SourcePreference   int         `json:"source_preference"`
	Fps                float64     `json:"fps"`
	Height             int         `json:"height"`
	Quality            int         `json:"quality"`
	Tbr                float64     `json:"tbr"`
	Url                string      `json:"url"`
	Width              int         `json:"width"`
	Language           string      `json:"language"`
	LanguagePreference int         `json:"language_preference"`
	Ext                string      `json:"ext"`
	Vcodec             string      `json:"vcodec"`
	Acodec             string      `json:"acodec"`
	DynamicRange       string      `json:"dynamic_range"`
	Protocol           string      `json:"protocol"`
	VideoExt           string      `json:"video_ext"`
	AudioExt           string      `json:"audio_ext"`
	Vbr                float64     `json:"vbr"`
	Abr                float64     `json:"abr"`
	Format             string      `json:"format"`
	Resolution         string      `json:"resolution"`
	FilesizeApprox     float64     `json:"filesize_approx"`
	HttpHeaders        struct {
		UserAgent      string `json:"User-Agent"`
		Accept         string `json:"Accept"`
		AcceptEncoding string `json:"Accept-Encoding"`
		AcceptLanguage string `json:"Accept-Language"`
	} `json:"http_headers"`
	Fulltitle string `json:"fulltitle"`
	Epoch     int    `json:"epoch"`
}

type Format struct {
	Asr               int         `json:"asr"`
	Filesize          int         `json:"filesize"`
	FormatID          string      `json:"format_id"`
	FormatNote        string      `json:"format_note"`
	Fps               interface{} `json:"fps"`
	Height            interface{} `json:"height"`
	Quality           int         `json:"quality"`
	Tbr               float64     `json:"tbr"`
	URL               string      `json:"url"`
	Width             interface{} `json:"width"`
	Ext               string      `json:"ext"`
	Vcodec            string      `json:"vcodec"`
	Acodec            string      `json:"acodec"`
	Abr               float64     `json:"abr,omitempty"`
	DownloaderOptions struct {
		HTTPChunkSize int `json:"http_chunk_size"`
	} `json:"downloader_options,omitempty"`
	Container   string `json:"container,omitempty"`
	Format      string `json:"format"`
	Protocol    string `json:"protocol"`
	HTTPHeaders struct {
		UserAgent      string `json:"User-Agent"`
		AcceptCharset  string `json:"Accept-Charset"`
		Accept         string `json:"Accept"`
		AcceptEncoding string `json:"Accept-Encoding"`
		AcceptLanguage string `json:"Accept-Language"`
	} `json:"http_headers"`
	Vbr float64 `json:"vbr,omitempty"`
}

type Thumbnail struct {
	URL        string `json:"url"`
	Preference int    `json:"preference"`
	ID         string `json:"id"`
	Height     int    `json:"height"`
	Width      int    `json:"width"`
	Resolution string `json:"resolution"`
}

type HTTPHeaders struct {
	AcceptCharset  string `json:"Accept-Charset"`
	AcceptLanguage string `json:"Accept-Language"`
	AcceptEncoding string `json:"Accept-Encoding"`
	Accept         string `json:"Accept"`
	UserAgent      string `json:"User-Agent"`
}
