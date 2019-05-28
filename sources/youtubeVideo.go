package sources

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"

	"github.com/lbryio/ytsync/namer"
	"github.com/lbryio/ytsync/sdk"
	"github.com/lbryio/ytsync/tagsManager"
	"github.com/lbryio/ytsync/thumbs"

	"github.com/ChannelMeter/iso8601duration"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/nikooo777/ytdl"
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
	maxVideoLength   float64
	publishedAt      time.Time
	dir              string
	youtubeInfo      *youtube.Video
	tags             []string
	awsConfig        aws.Config
	thumbnailURL     string
	lbryChannelID    string
}

const reflectorURL = "http://blobs.lbry.io/"

var youtubeCategories = map[string]string{
	"1":  "Film & Animation",
	"2":  "Autos & Vehicles",
	"10": "Music",
	"15": "Pets & Animals",
	"17": "Sports",
	"18": "Short Movies",
	"19": "Travel & Events",
	"20": "Gaming",
	"21": "Videoblogging",
	"22": "People & Blogs",
	"23": "Comedy",
	"24": "Entertainment",
	"25": "News & Politics",
	"26": "Howto & Style",
	"27": "Education",
	"28": "Science & Technology",
	"29": "Nonprofits & Activism",
	"30": "Movies",
	"31": "Anime/Animation",
	"32": "Action/Adventure",
	"33": "Classics",
	"34": "Comedy",
	"35": "Documentary",
	"36": "Drama",
	"37": "Family",
	"38": "Foreign",
	"39": "Horror",
	"40": "Sci-Fi/Fantasy",
	"41": "Thriller",
	"42": "Shorts",
	"43": "Shows",
	"44": "Trailers",
}

func NewYoutubeVideo(directory string, videoData *youtube.Video, playlistPosition int64, awsConfig aws.Config) *YoutubeVideo {
	publishedAt, _ := time.Parse(time.RFC3339Nano, videoData.Snippet.PublishedAt) // ignore parse errors
	return &YoutubeVideo{
		id:               videoData.Id,
		title:            videoData.Snippet.Title,
		description:      videoData.Snippet.Description,
		channelTitle:     videoData.Snippet.ChannelTitle,
		playlistPosition: playlistPosition,
		publishedAt:      publishedAt,
		dir:              directory,
		youtubeInfo:      videoData,
		awsConfig:        awsConfig,
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

func (v *YoutubeVideo) getFullPath() string {
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
	additionalDescription := "\nhttps://www.youtube.com/watch?v=" + v.id
	khanAcademyClaimID := "5fc52291980268b82413ca4c0ace1b8d749f3ffb"
	if v.lbryChannelID == khanAcademyClaimID {
		additionalDescription = additionalDescription + "\nNote: All Khan Academy content is available for free at (www.khanacademy.org)"
	}
	return strings.Join(strings.Split(description, "\n")[:maxLines], "\n") + "\n..."
}

func (v *YoutubeVideo) fallbackDownload() error {
	cmd := exec.Command("youtube-dl",
		"--id "+v.ID(),
		"--no-progress",
		"-f \"bestvideo[ext=mp4,height<=1080,filesize<1000M]+bestaudio/best[ext=mp4,height<=1080,filesize<1000M]\"",
		"-o "+strings.TrimRight(v.getFullPath(), ".mp4"))
	log.Printf("Running command and waiting for it to finish...")
	err := cmd.Run()
	if err != nil {
		log.Printf("Command finished with error: %v", err)
		return errors.Err(err)
	}
	return nil
}

func (v *YoutubeVideo) download() error {
	videoPath := v.getFullPath()

	err := os.Mkdir(v.videoDir(), 0750)
	if err != nil && !strings.Contains(err.Error(), "file exists") {
		return errors.Wrap(err, 0)
	}

	_, err = os.Stat(videoPath)
	if err != nil && !os.IsNotExist(err) {
		return errors.Err(err)
	} else if err == nil {
		log.Debugln(v.id + " already exists at " + videoPath)
		return nil
	}

	videoUrl := "https://www.youtube.com/watch?v=" + v.id
	videoInfo, err := ytdl.GetVideoInfo(videoUrl)
	if err != nil {
		return errors.Err(err)
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
	isLengthLimitSet := v.maxVideoLength > 0.01
	if isLengthLimitSet && videoInfo.Duration.Hours() > v.maxVideoLength {
		return errors.Err("video is too long to process")
	}

	for i := 0; i < len(formats) && i < maxRetryAttempts; i++ {
		formatIndex := i
		if i == maxRetryAttempts-1 {
			formatIndex = len(formats) - 1
		}
		var downloadedFile *os.File
		downloadedFile, err = os.Create(videoPath)
		if err != nil {
			return errors.Err(err)
		}
		err = videoInfo.Download(formats[formatIndex], downloadedFile)
		_ = downloadedFile.Close()
		if err != nil {
			//delete the video and ignore the error
			_ = v.delete()
			return errors.Err(err.Error())
		}
		fi, err := os.Stat(v.getFullPath())
		if err != nil {
			return errors.Err(err)
		}
		videoSize := fi.Size()
		v.size = &videoSize

		isVideoSizeLimitSet := v.maxVideoSize > 0
		if isVideoSizeLimitSet && videoSize > v.maxVideoSize {
			//delete the video and ignore the error
			_ = v.delete()
			err = errors.Err("file is too big and there is no other format available")
		} else {
			break
		}
	}
	return errors.Err(err)
}

func (v *YoutubeVideo) videoDir() string {
	return v.dir + "/" + v.id
}
func (v *YoutubeVideo) getDownloadedPath() (string, error) {
	files, err := ioutil.ReadDir(v.videoDir())
	if err != nil {
		err = errors.Prefix("list error", err)
		log.Errorln(err)
		return "", err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if strings.Contains(v.getFullPath(), f.Name()) {
			return v.videoDir() + "/" + f.Name(), nil
		}
	}
	return "", errors.Err("could not find any downloaded videos")

}
func (v *YoutubeVideo) delete() error {
	videoPath, err := v.getDownloadedPath()
	if err != nil {
		log.Errorln(err)
		return err
	}
	err = os.Remove(videoPath)
	log.Debugf("%s deleted from disk (%s)", v.id, videoPath)

	if err != nil {
		err = errors.Prefix("delete error", err)
		log.Errorln(err)
		return err
	}

	return nil
}

func (v *YoutubeVideo) triggerThumbnailSave() (err error) {
	thumbnail := thumbs.GetBestThumbnail(v.youtubeInfo.Snippet.Thumbnails)
	v.thumbnailURL, err = thumbs.MirrorThumbnail(thumbnail.Url, v.ID(), v.awsConfig)
	return err
}

func (v *YoutubeVideo) publish(daemon *jsonrpc.Client, claimAddress string, amount float64, namer *namer.Namer) (*SyncSummary, error) {
	languages, locations, tags := v.getMetadata()

	options := jsonrpc.StreamCreateOptions{
		ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
			Title:        v.title,
			Description:  v.getAbbrevDescription(),
			ClaimAddress: &claimAddress,
			Languages:    languages,
			ThumbnailURL: &v.thumbnailURL,
			Tags:         tags,
			Locations:    locations,
		},
		Author:      util.PtrToString(v.channelTitle),
		License:     util.PtrToString("Copyrighted (contact author)"),
		ReleaseTime: util.PtrToInt64(v.publishedAt.Unix()),
		ChannelID:   &v.lbryChannelID,
	}
	downloadPath, err := v.getDownloadedPath()
	if err != nil {
		return nil, err
	}
	return publishAndRetryExistingNames(daemon, v.title, downloadPath, amount, options, namer)
}

func (v *YoutubeVideo) Size() *int64 {
	return v.size
}

type SyncParams struct {
	ClaimAddress   string
	Amount         float64
	ChannelID      string
	MaxVideoSize   int
	Namer          *namer.Namer
	MaxVideoLength float64
}

func (v *YoutubeVideo) Sync(daemon *jsonrpc.Client, params SyncParams, existingVideoData *sdk.SyncedVideo, reprocess bool) (*SyncSummary, error) {
	v.maxVideoSize = int64(params.MaxVideoSize) * 1024 * 1024
	v.maxVideoLength = params.MaxVideoLength
	v.lbryChannelID = params.ChannelID
	if reprocess && existingVideoData != nil && existingVideoData.Published {
		summary, err := v.reprocess(daemon, params, existingVideoData)
		return summary, err
	}
	return v.downloadAndPublish(daemon, params)
}

func (v *YoutubeVideo) downloadAndPublish(daemon *jsonrpc.Client, params SyncParams) (*SyncSummary, error) {
	err := v.download()
	if err != nil {
		log.Errorf("standard downloader failed: %s. Trying fallback downloader\n", err.Error())
		fallBackErr := v.fallbackDownload()
		if fallBackErr != nil {
			log.Errorf("fallback downloader failed: %s\n", err.Error())
			return nil, errors.Prefix("download error", err) //return original error
		}
	}
	log.Debugln("Downloaded " + v.id)

	err = v.triggerThumbnailSave()
	if err != nil {
		return nil, errors.Prefix("thumbnail error", err)
	}
	log.Debugln("Created thumbnail for " + v.id)

	summary, err := v.publish(daemon, params.ClaimAddress, params.Amount, params.Namer)
	//delete the video in all cases (and ignore the error)
	_ = v.delete()

	return summary, errors.Prefix("publish error", err)
}

func (v *YoutubeVideo) getMetadata() (languages []string, locations []jsonrpc.Location, tags []string) {
	languages = nil
	if v.youtubeInfo.Snippet.DefaultLanguage != "" {
		languages = []string{v.youtubeInfo.Snippet.DefaultLanguage}
	}

	locations = nil
	if v.youtubeInfo.RecordingDetails != nil && v.youtubeInfo.RecordingDetails.Location != nil {
		locations = []jsonrpc.Location{{
			Latitude:  util.PtrToString(fmt.Sprintf("%.7f", v.youtubeInfo.RecordingDetails.Location.Latitude)),
			Longitude: util.PtrToString(fmt.Sprintf("%.7f", v.youtubeInfo.RecordingDetails.Location.Longitude)),
		}}
	}
	tags = append([]string{youtubeCategories[v.youtubeInfo.Snippet.CategoryId]}, v.youtubeInfo.Snippet.Tags...)
	tags = tagsManager.SanitizeTags(tags)
	return languages, locations, tags
}

func (v *YoutubeVideo) reprocess(daemon *jsonrpc.Client, params SyncParams, existingVideoData *sdk.SyncedVideo) (*SyncSummary, error) {
	c, err := daemon.ClaimSearch(nil, &existingVideoData.ClaimID, nil, nil)
	if err != nil {
		return nil, errors.Err(err)
	}
	if len(c.Claims) == 0 {
		return nil, errors.Err("cannot reprocess: no claim found for this video")
	} else if len(c.Claims) > 1 {
		return nil, errors.Err("cannot reprocess: too many claims. claimID: %s", existingVideoData.ClaimID)
	}

	currentClaim := c.Claims[0]
	languages, locations, tags := v.getMetadata()

	thumbnailURL := ""
	if currentClaim.Value.GetThumbnail() == nil {
		thumbnail := thumbs.GetBestThumbnail(v.youtubeInfo.Snippet.Thumbnails)
		thumbnailURL, err = thumbs.MirrorThumbnail(thumbnail.Url, v.ID(), v.awsConfig)
	} else {
		thumbnailURL = thumbs.ThumbnailEndpoint + v.ID()
	}

	videoSize, err := currentClaim.GetStreamSizeByMagic()
	if err != nil {
		if existingVideoData.Size > 0 {
			videoSize = uint64(existingVideoData.Size)
		} else {
			log.Infof("%s: the video must be republished as we can't get the right size", v.ID())
			//return v.downloadAndPublish(daemon, params) //TODO: actually republish the video. NB: the current claim should be abandoned first
			return nil, errors.Err("the video must be republished as we can't get the right size")
		}
	}

	videoDuration, err := duration.FromString(v.youtubeInfo.ContentDetails.Duration)
	if err != nil {
		return nil, errors.Err(err)
	}

	pr, err := daemon.StreamUpdate(existingVideoData.ClaimID, jsonrpc.StreamUpdateOptions{
		StreamCreateOptions: &jsonrpc.StreamCreateOptions{
			ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
				Title:        v.title,
				Description:  v.getAbbrevDescription(),
				Tags:         tags,
				Languages:    languages,
				Locations:    locations,
				ThumbnailURL: &thumbnailURL,
			},
			Author:      util.PtrToString(""),
			License:     util.PtrToString("Copyrighted (contact author)"),
			ReleaseTime: util.PtrToInt64(v.publishedAt.Unix()),
			Duration:    util.PtrToUint64(uint64(math.Ceil(videoDuration.ToDuration().Seconds()))),
			ChannelID:   &v.lbryChannelID,
		},
		FileSize: &videoSize,
	})
	if err != nil {
		return nil, err
	}

	return &SyncSummary{
		ClaimID:   pr.Outputs[0].ClaimID,
		ClaimName: pr.Outputs[0].Name,
	}, nil
}
