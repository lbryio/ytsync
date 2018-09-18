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

	"sync"

	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/jsonrpc"

	"github.com/rylio/ytdl"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/youtube/v3"
)

type YoutubeVideo struct {
	id               string
	channelTitle     string
	title            string
	description      string
	playlistPosition int64
	size             *int64
	maxVideoSize     int64
	publishedAt      time.Time
	dir              string
	claimNames       map[string]bool
	syncedVideosMux  *sync.RWMutex
}

func NewYoutubeVideo(directory string, snippet *youtube.PlaylistItemSnippet) *YoutubeVideo {
	publishedAt, _ := time.Parse(time.RFC3339Nano, snippet.PublishedAt) // ignore parse errors
	return &YoutubeVideo{
		id:               snippet.ResourceId.VideoId,
		title:            snippet.Title,
		description:      snippet.Description,
		channelTitle:     snippet.ChannelTitle,
		playlistPosition: snippet.Position,
		publishedAt:      publishedAt,
		dir:              directory,
	}
}

func (v *YoutubeVideo) ID() string {
	return v.id
}

func (v *YoutubeVideo) PlaylistPosition() int {
	return int(v.playlistPosition)
}

func (v *YoutubeVideo) IDAndNum() string {
	return v.ID() + " (" + strconv.Itoa(int(v.playlistPosition)) + " in channel)"
}

func (v *YoutubeVideo) PublishedAt() time.Time {
	return v.publishedAt
}

func (v *YoutubeVideo) getFilename() string {
	maxLen := 30
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)

	chunks := strings.Split(strings.ToLower(strings.Trim(reg.ReplaceAllString(v.title, "-"), "-")), "-")

	name := chunks[0]
	if len(name) > maxLen {
		name = name[:maxLen]
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
	if len(name) < 1 {
		name = v.id
	}
	return v.videoDir() + "/" + name + ".mp4"
}

func (v *YoutubeVideo) getAbbrevDescription() string {
	maxLines := 10
	description := strings.TrimSpace(v.description)
	if strings.Count(description, "\n") < maxLines {
		return description
	}
	return strings.Join(strings.Split(description, "\n")[:maxLines], "\n") + "\n..."
}

func (v *YoutubeVideo) download() error {
	videoPath := v.getFilename()

	err := os.Mkdir(v.videoDir(), 0750)
	if err != nil && !strings.Contains(err.Error(), "file exists") {
		return errors.Wrap(err, 0)
	}

	_, err = os.Stat(videoPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if err == nil {
		log.Debugln(v.id + " already exists at " + videoPath)
		return nil
	}

	videoUrl := "https://www.youtube.com/watch?v=" + v.id
	videoInfo, err := ytdl.GetVideoInfo(videoUrl)
	if err != nil {
		return err
	}

	codec := []string{"H.264"}
	ext := []string{"mp4"}

	//Filter requires a [] interface{}
	codecFilter := make([]interface{}, len(codec))
	for i, v := range codec {
		codecFilter[i] = v
	}

	//Filter requires a [] interface{}
	extFilter := make([]interface{}, len(ext))
	for i, v := range ext {
		extFilter[i] = v
	}

	formats := videoInfo.Formats.Filter(ytdl.FormatVideoEncodingKey, codecFilter).Filter(ytdl.FormatExtensionKey, extFilter)
	if len(formats) == 0 {
		return errors.Err("no compatible format available for this video")
	}
	maxRetryAttempts := 5
	for i := 0; i < len(formats) && i < maxRetryAttempts; i++ {
		formatIndex := i
		if i == maxRetryAttempts-1 {
			formatIndex = len(formats) - 1
		}
		var downloadedFile *os.File
		downloadedFile, err = os.Create(videoPath)
		if err != nil {
			return err
		}
		err = videoInfo.Download(formats[formatIndex], downloadedFile)
		downloadedFile.Close()
		if err != nil {
			break
		}
		fi, err := os.Stat(v.getFilename())
		if err != nil {
			return err
		}
		videoSize := fi.Size()
		v.size = &videoSize

		if videoSize > v.maxVideoSize {
			//delete the video and ignore the error
			_ = v.delete()
			err = errors.Err("file is too big and there is no other format available")
		} else {
			break
		}
	}

	return err
}

func (v *YoutubeVideo) videoDir() string {
	return v.dir + "/" + v.id
}

func (v *YoutubeVideo) delete() error {
	videoPath := v.getFilename()
	err := os.Remove(videoPath)
	if err != nil {
		log.Errorln(errors.Prefix("delete error", err))
		return err
	}
	log.Debugln(v.id + " deleted from disk (" + videoPath + ")")
	return nil
}

func (v *YoutubeVideo) triggerThumbnailSave() error {
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
		Error   int    `json:"error"`
		Url     string `json:"url,omitempty"`
		Message string `json:"message,omitempty"`
	}
	err = json.Unmarshal(contents, &decoded)
	if err != nil {
		return err
	}

	if decoded.Error != 0 {
		return errors.Err("error creating thumbnail: " + decoded.Message)
	}

	return nil
}

func strPtr(s string) *string { return &s }

func (v *YoutubeVideo) publish(daemon *jsonrpc.Client, claimAddress string, amount float64, channelID string) (*SyncSummary, error) {
	if channelID == "" {
		return nil, errors.Err("a claim_id for the channel wasn't provided") //TODO: this is probably not needed?
	}
	options := jsonrpc.PublishOptions{
		Title:         &v.title,
		Author:        &v.channelTitle,
		Description:   strPtr(v.getAbbrevDescription() + "\nhttps://www.youtube.com/watch?v=" + v.id),
		Language:      strPtr("en"),
		ClaimAddress:  &claimAddress,
		Thumbnail:     strPtr("https://berk.ninja/thumbnails/" + v.id),
		License:       strPtr("Copyrighted (contact author)"),
		ChangeAddress: &claimAddress,
		ChannelID:     &channelID,
	}
	return publishAndRetryExistingNames(daemon, v.title, v.getFilename(), amount, options, v.claimNames, v.syncedVideosMux)
}

func (v *YoutubeVideo) Size() *int64 {
	return v.size
}

func (v *YoutubeVideo) Sync(daemon *jsonrpc.Client, claimAddress string, amount float64, channelID string, maxVideoSize int, claimNames map[string]bool, syncedVideosMux *sync.RWMutex) (*SyncSummary, error) {
	v.claimNames = claimNames
	v.syncedVideosMux = syncedVideosMux
	v.maxVideoSize = int64(maxVideoSize) * 1024 * 1024

	//download and thumbnail can be done in parallel
	err := v.download()
	if err != nil {
		return nil, errors.Prefix("download error", err)
	}
	log.Debugln("Downloaded " + v.id)

	err = v.triggerThumbnailSave()
	if err != nil {
		return nil, errors.Prefix("thumbnail error", err)
	}
	log.Debugln("Created thumbnail for " + v.id)

	summary, err := v.publish(daemon, claimAddress, amount, channelID)
	//delete the video in all cases (and ignore the error)
	_ = v.delete()
	if err != nil {
		return nil, errors.Prefix("publish error", err)
	}

	return summary, nil
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
