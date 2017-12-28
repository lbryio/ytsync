package sources

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lbryio/lbry.go/jsonrpc"

	"github.com/go-errors/errors"
	ytdl "github.com/kkdai/youtube"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/youtube/v3"
)

type YoutubeVideo struct {
	id               string
	channelTitle     string
	title            string
	description      string
	playlistPosition int64
	publishedAt      time.Time
	dir              string
}

func NewYoutubeVideo(directory string, snippet *youtube.PlaylistItemSnippet) YoutubeVideo {
	publishedAt, _ := time.Parse(time.RFC3339Nano, snippet.PublishedAt) // ignore parse errors
	return YoutubeVideo{
		id:               snippet.ResourceId.VideoId,
		title:            snippet.Title,
		description:      snippet.Description,
		channelTitle:     snippet.ChannelTitle,
		playlistPosition: snippet.Position,
		publishedAt:      publishedAt,
		dir:              directory,
	}
}

func (v YoutubeVideo) ID() string {
	return v.id
}

func (v YoutubeVideo) IDAndNum() string {
	return v.ID() + " (" + strconv.Itoa(int(v.playlistPosition)) + " in channel)"
}

func (v YoutubeVideo) PublishedAt() time.Time {
	return v.publishedAt
}

func (v YoutubeVideo) getFilename() string {
	return v.dir + "/" + v.id + ".mp4"
}

func (v YoutubeVideo) getClaimName() string {
	maxLen := 40
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)

	chunks := strings.Split(strings.ToLower(strings.Trim(reg.ReplaceAllString(v.title, "-"), "-")), "-")

	name := chunks[0]
	if len(name) > maxLen {
		return name[:maxLen]
	}

	for _, chunk := range chunks[1:] {
		tmpName := name + "-" + chunk
		if len(tmpName) > maxLen {
			if len(name) < 20 {
				name = tmpName[:maxLen]
			}
			break
		}
		name = tmpName
	}

	return name
}

func (v YoutubeVideo) getAbbrevDescription() string {
	maxLines := 10
	description := strings.TrimSpace(v.description)
	if strings.Count(description, "\n") < maxLines {
		return description
	}
	return strings.Join(strings.Split(description, "\n")[:maxLines], "\n") + "\n..."
}

func (v YoutubeVideo) Download() error {
	videoPath := v.getFilename()

	_, err := os.Stat(videoPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if err == nil {
		log.Debugln(v.id + " already exists at " + videoPath)
		return nil
	}

	downloader := ytdl.NewYoutube(false)
	err = downloader.DecodeURL("https://www.youtube.com/watch?v=" + v.id)
	if err != nil {
		return err
	}
	err = downloader.StartDownload(videoPath)
	if err != nil {
		return err
	}
	return nil
}

func (v YoutubeVideo) TriggerThumbnailSave() error {
	client := &http.Client{Timeout: 30 * time.Second}

	params, err := json.Marshal(map[string]string{"videoid": v.id})
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPut, "https://jgp4g1qoud.execute-api.us-east-1.amazonaws.com/prod/thumbnail", bytes.NewBuffer(params))
	if err != nil {
		return err
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	var decoded struct {
		error   int    `json:"error"`
		url     string `json:"url,omitempty"`
		message string `json:"message,omitempty"`
	}
	err = json.Unmarshal(contents, &decoded)
	if err != nil {
		return err
	}

	if decoded.error != 0 {
		return errors.New("error creating thumbnail: " + decoded.message)
	}

	return nil
}

func strPtr(s string) *string { return &s }

func (v YoutubeVideo) Publish(daemon *jsonrpc.Client, claimAddress string, amount float64, channelName string) error {
	options := jsonrpc.PublishOptions{
		Title:        &v.title,
		Author:       &v.channelTitle,
		Description:  strPtr(v.getAbbrevDescription() + "\nhttps://www.youtube.com/watch?v=" + v.id),
		Language:     strPtr("en"),
		ClaimAddress: &claimAddress,
		Thumbnail:    strPtr("http://berk.ninja/thumbnails/" + v.id),
		License:      strPtr("Copyrighted (contact author)"),
	}
	if channelName != "" {
		options.ChannelName = &channelName
	}

	_, err := daemon.Publish(v.getClaimName(), v.getFilename(), amount, options)
	return err
}

func (v YoutubeVideo) Sync(daemon *jsonrpc.Client, claimAddress string, amount float64, channelName string) error {
	//download and thumbnail can be done in parallel
	err := v.Download()
	if err != nil {
		return errors.WrapPrefix(err, "download error", 0)
	}
	log.Debugln("Downloaded " + v.id)

	err = v.TriggerThumbnailSave()
	if err != nil {
		return errors.WrapPrefix(err, "thumbnail error", 0)
	}
	log.Debugln("Created thumbnail for " + v.id)

	err = v.Publish(daemon, claimAddress, amount, channelName)
	if err != nil {
		return errors.WrapPrefix(err, "publish error", 0)
	}

	return nil
}

// sorting videos
//type ByPublishedAt []YoutubeVideo
//
//func (a ByPublishedAt) Len() int           { return len(a) }
//func (a ByPublishedAt) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
//func (a ByPublishedAt) Less(i, j int) bool { return a[i].publishedAt.Before(a[j].publishedAt) }
//
//type ByPlaylistPosition []YoutubeVideo
//
//func (a ByPlaylistPosition) Len() int           { return len(a) }
//func (a ByPlaylistPosition) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
//func (a ByPlaylistPosition) Less(i, j int) bool { return a[i].playlistPosition < a[j].playlistPosition }
