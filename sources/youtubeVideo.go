package sources

import (
	"fmt"
	"io/ioutil"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"

	"github.com/lbryio/ytsync/namer"
	"github.com/lbryio/ytsync/sdk"
	"github.com/lbryio/ytsync/tagsManager"
	"github.com/lbryio/ytsync/thumbs"

	"github.com/ChannelMeter/iso8601duration"
	"github.com/asaskevich/govalidator"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/youtube/v3"
)

type YoutubeVideo struct {
	id               string
	title            string
	description      string
	playlistPosition int64
	size             *int64
	maxVideoSize     int64
	maxVideoLength   float64
	publishedAt      time.Time
	dir              string
	youtubeInfo      *youtube.Video
	youtubeChannelID string
	tags             []string
	awsConfig        aws.Config
	thumbnailURL     string
	lbryChannelID    string
	mocked           bool
	walletLock       *sync.RWMutex
}

const reflectorURL = "http://blobs.lbry.io/"

var youtubeCategories = map[string]string{
	"1":  "film & animation",
	"2":  "autos & vehicles",
	"10": "music",
	"15": "pets & animals",
	"17": "sports",
	"18": "short movies",
	"19": "travel & events",
	"20": "gaming",
	"21": "videoblogging",
	"22": "people & blogs",
	"23": "comedy",
	"24": "entertainment",
	"25": "news & politics",
	"26": "howto & style",
	"27": "education",
	"28": "science & technology",
	"29": "nonprofits & activism",
	"30": "movies",
	"31": "anime/animation",
	"32": "action/adventure",
	"33": "classics",
	"34": "comedy",
	"35": "documentary",
	"36": "drama",
	"37": "family",
	"38": "foreign",
	"39": "horror",
	"40": "sci-fi/fantasy",
	"41": "thriller",
	"42": "shorts",
	"43": "shows",
	"44": "trailers",
}

func NewYoutubeVideo(directory string, videoData *youtube.Video, playlistPosition int64, awsConfig aws.Config) *YoutubeVideo {
	publishedAt, _ := time.Parse(time.RFC3339Nano, videoData.Snippet.PublishedAt) // ignore parse errors
	return &YoutubeVideo{
		id:               videoData.Id,
		title:            videoData.Snippet.Title,
		description:      videoData.Snippet.Description,
		playlistPosition: playlistPosition,
		publishedAt:      publishedAt,
		dir:              directory,
		youtubeInfo:      videoData,
		awsConfig:        awsConfig,
		mocked:           false,
		youtubeChannelID: videoData.Snippet.ChannelId,
	}
}
func NewMockedVideo(directory string, videoID string, youtubeChannelID string, awsConfig aws.Config) *YoutubeVideo {
	return &YoutubeVideo{
		id:               videoID,
		playlistPosition: 0,
		dir:              directory,
		awsConfig:        awsConfig,
		mocked:           true,
		youtubeChannelID: youtubeChannelID,
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
	if v.mocked {
		return time.Unix(0, 0)
	}
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
	additionalDescription := "\nhttps://www.youtube.com/watch?v=" + v.id
	khanAcademyClaimID := "5fc52291980268b82413ca4c0ace1b8d749f3ffb"
	if v.lbryChannelID == khanAcademyClaimID {
		additionalDescription = additionalDescription + "\nNote: All Khan Academy content is available for free at (www.khanacademy.org)"
	}
	if strings.Count(description, "\n") < maxLines {
		return description + "\n..." + additionalDescription
	}
	return strings.Join(strings.Split(description, "\n")[:maxLines], "\n") + "\n..." + additionalDescription
}

var ipPool []string
var IPIndex int
var ipMutex sync.Mutex

func getNextIP() (string, error) {
	ipMutex.Lock()
	defer ipMutex.Unlock()
	if len(ipPool) < 1 {
		IPIndex = 0
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", errors.Err(err)
		}

		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To16() != nil && govalidator.IsIPv6(ipnet.IP.String()) {
					ipPool = append(ipPool, ipnet.IP.String())
				}
			}
		}
	}
	nextIP := ipPool[IPIndex]
	if IPIndex == len(ipPool)-1 {
		IPIndex = 0
	} else {
		IPIndex++
	}
	return nextIP, nil
}

func (v *YoutubeVideo) download(useIPv6 bool) error {
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

	ytdlArgs := []string{
		"--no-progress",
		"--max-filesize",
		fmt.Sprintf("%dM", v.maxVideoSize),
		"-fbestvideo[ext=mp4][height<=1080]+bestaudio[ext!=webm]",
		"-o" + strings.TrimRight(v.getFullPath(), ".mp4"),
		"--merge-output-format",
		"mp4",
	}
	if useIPv6 {
		sourceAddress, err := getNextIP()
		if err != nil {
			return errors.Err(err)
		}
		log.Infof("using IPv6: %s", sourceAddress)
		ytdlArgs = append(ytdlArgs,
			"-6",
			"--source-address",
			sourceAddress,
		)
	} else {
		log.Infoln("using IPv4")
		ytdlArgs = append(ytdlArgs, "-4")
	}
	ytdlArgs = append(ytdlArgs, "https://www.youtube.com/watch?v="+v.ID())
	cmd := exec.Command("youtube-dl", ytdlArgs...)

	log.Printf("Running command and waiting for it to finish...")

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errors.Err(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Err(err)
	}

	if err := cmd.Start(); err != nil {
		return errors.Err(err)
	}

	errorLog, _ := ioutil.ReadAll(stderr)
	outLog, _ := ioutil.ReadAll(stdout)

	if err = cmd.Wait(); err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return errors.Err(string(errorLog))
		}
		return errors.Err(err)
	}

	log.Debugln(string(outLog))

	if string(errorLog) != "" {
		log.Printf("Command finished with error: %v", errors.Err(string(errorLog)))
		_ = v.delete()
		return errors.Err(string(errorLog))
	}
	fi, err := os.Stat(v.getFullPath())
	if err != nil {
		return errors.Err(err)
	}
	videoSize := fi.Size()
	v.size = &videoSize
	return nil
}

func (v *YoutubeVideo) videoDir() string {
	return v.dir + "/" + v.id
}
func (v *YoutubeVideo) getDownloadedPath() (string, error) {
	files, err := ioutil.ReadDir(v.videoDir())
	log.Infoln(v.videoDir())
	if err != nil {
		err = errors.Prefix("list error", err)
		log.Errorln(err)
		return "", err
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if strings.Contains(v.getFullPath(), strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))) {
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

func (v *YoutubeVideo) publish(daemon *jsonrpc.Client, params SyncParams) (*SyncSummary, error) {
	languages, locations, tags := v.getMetadata()

	var fee *jsonrpc.Fee
	if params.Fee != nil {
		feeAmount, err := decimal.NewFromString(params.Fee.Amount)
		if err != nil {
			return nil, errors.Err(err)
		}
		fee = &jsonrpc.Fee{
			FeeAddress:  &params.Fee.Address,
			FeeAmount:   feeAmount,
			FeeCurrency: jsonrpc.Currency(params.Fee.Currency),
		}
	}

	options := jsonrpc.StreamCreateOptions{
		ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
			Title:        &v.title,
			Description:  util.PtrToString(v.getAbbrevDescription()),
			ClaimAddress: &params.ClaimAddress,
			Languages:    languages,
			ThumbnailURL: &v.thumbnailURL,
			Tags:         tags,
			Locations:    locations,
		},
		Fee:         fee,
		License:     util.PtrToString("Copyrighted (contact publisher)"),
		ReleaseTime: util.PtrToInt64(v.publishedAt.Unix()),
		ChannelID:   &v.lbryChannelID,
	}
	downloadPath, err := v.getDownloadedPath()
	if err != nil {
		return nil, err
	}
	return publishAndRetryExistingNames(daemon, v.title, downloadPath, params.Amount, options, params.Namer, v.walletLock)
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
	Fee            *sdk.Fee
}

func (v *YoutubeVideo) Sync(daemon *jsonrpc.Client, params SyncParams, existingVideoData *sdk.SyncedVideo, reprocess bool, walletLock *sync.RWMutex) (*SyncSummary, error) {
	v.maxVideoSize = int64(params.MaxVideoSize)
	v.maxVideoLength = params.MaxVideoLength
	v.lbryChannelID = params.ChannelID
	v.walletLock = walletLock
	if reprocess && existingVideoData != nil && existingVideoData.Published {
		summary, err := v.reprocess(daemon, params, existingVideoData)
		return summary, errors.Prefix("upgrade failed", err)
	}
	return v.downloadAndPublish(daemon, params)
}

var isThrottled bool

func (v *YoutubeVideo) downloadAndPublish(daemon *jsonrpc.Client, params SyncParams) (*SyncSummary, error) {
	err := v.download(isThrottled)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP Error 429") && !isThrottled {
			isThrottled = true
			err = v.download(isThrottled)
			if err != nil {
				return nil, errors.Prefix("download error", err)
			}
		} else {
			return nil, errors.Prefix("download error", err)
		}
	}
	log.Debugln("Downloaded " + v.id)

	err = v.triggerThumbnailSave()
	if err != nil {
		return nil, errors.Prefix("thumbnail error", err)
	}
	log.Debugln("Created thumbnail for " + v.id)

	summary, err := v.publish(daemon, params)
	//delete the video in all cases (and ignore the error)
	_ = v.delete()

	return summary, errors.Prefix("publish error", err)
}

func (v *YoutubeVideo) getMetadata() (languages []string, locations []jsonrpc.Location, tags []string) {
	languages = nil
	locations = nil
	tags = nil
	if !v.mocked {
		if v.youtubeInfo.Snippet.DefaultLanguage != "" {
			languages = []string{v.youtubeInfo.Snippet.DefaultLanguage}
		}

		if v.youtubeInfo.RecordingDetails != nil && v.youtubeInfo.RecordingDetails.Location != nil {
			locations = []jsonrpc.Location{{
				Latitude:  util.PtrToString(fmt.Sprintf("%.7f", v.youtubeInfo.RecordingDetails.Location.Latitude)),
				Longitude: util.PtrToString(fmt.Sprintf("%.7f", v.youtubeInfo.RecordingDetails.Location.Longitude)),
			}}
		}
		tags = v.youtubeInfo.Snippet.Tags
	}
	tags, err := tagsManager.SanitizeTags(tags, v.youtubeChannelID)
	if err != nil {
		log.Errorln(err.Error())
	}
	if !v.mocked {
		tags = append(tags, youtubeCategories[v.youtubeInfo.Snippet.CategoryId])
	}

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
		if v.mocked {
			return nil, errors.Err("could not find thumbnail for mocked video")
		}
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
	v.size = util.PtrToInt64(int64(videoSize))
	var fee *jsonrpc.Fee
	if params.Fee != nil {
		feeAmount, err := decimal.NewFromString(params.Fee.Amount)
		if err != nil {
			return nil, errors.Err(err)
		}
		fee = &jsonrpc.Fee{
			FeeAddress:  &params.Fee.Address,
			FeeAmount:   feeAmount,
			FeeCurrency: jsonrpc.Currency(params.Fee.Currency),
		}
	}
	streamCreateOptions := &jsonrpc.StreamCreateOptions{
		ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
			Tags:         tags,
			ThumbnailURL: &thumbnailURL,
			Languages:    languages,
			Locations:    locations,
		},
		Author:    util.PtrToString(""),
		License:   util.PtrToString("Copyrighted (contact publisher)"),
		ChannelID: &v.lbryChannelID,
		Height:    util.PtrToUint(720),
		Width:     util.PtrToUint(1280),
		Fee:       fee,
	}

	v.walletLock.RLock()
	defer v.walletLock.RUnlock()
	if v.mocked {
		pr, err := daemon.StreamUpdate(existingVideoData.ClaimID, jsonrpc.StreamUpdateOptions{
			StreamCreateOptions: streamCreateOptions,
			FileSize:            &videoSize,
		})
		if err != nil {
			return nil, err
		}

		return &SyncSummary{
			ClaimID:   pr.Outputs[0].ClaimID,
			ClaimName: pr.Outputs[0].Name,
		}, nil
	}

	videoDuration, err := duration.FromString(v.youtubeInfo.ContentDetails.Duration)
	if err != nil {
		return nil, errors.Err(err)
	}

	streamCreateOptions.ClaimCreateOptions.Title = &v.title
	streamCreateOptions.ClaimCreateOptions.Description = util.PtrToString(v.getAbbrevDescription())
	streamCreateOptions.Duration = util.PtrToUint64(uint64(math.Ceil(videoDuration.ToDuration().Seconds())))
	streamCreateOptions.ReleaseTime = util.PtrToInt64(v.publishedAt.Unix())
	pr, err := daemon.StreamUpdate(existingVideoData.ClaimID, jsonrpc.StreamUpdateOptions{
		ClearLanguages:      util.PtrToBool(true),
		ClearLocations:      util.PtrToBool(true),
		ClearTags:           util.PtrToBool(true),
		StreamCreateOptions: streamCreateOptions,
		FileSize:            &videoSize,
	})
	if err != nil {
		return nil, err
	}

	return &SyncSummary{
		ClaimID:   pr.Outputs[0].ClaimID,
		ClaimName: pr.Outputs[0].Name,
	}, nil
}
