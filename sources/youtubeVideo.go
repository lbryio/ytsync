package sources

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"

	"github.com/lbryio/ytsync/v5/ip_manager"
	"github.com/lbryio/ytsync/v5/namer"
	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/lbryio/ytsync/v5/tags_manager"
	"github.com/lbryio/ytsync/v5/thumbs"
	"github.com/lbryio/ytsync/v5/timing"
	logUtils "github.com/lbryio/ytsync/v5/util"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/stop"
	"github.com/lbryio/lbry.go/v2/extras/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

type YoutubeVideo struct {
	id               string
	title            string
	description      string
	playlistPosition int64
	size             *int64
	maxVideoSize     int64
	maxVideoLength   time.Duration
	publishedAt      time.Time
	dir              string
	youtubeInfo      *ytdl.YtdlVideo
	youtubeChannelID string
	tags             []string
	awsConfig        aws.Config
	thumbnailURL     string
	lbryChannelID    string
	mocked           bool
	walletLock       *sync.RWMutex
	stopGroup        *stop.Group
	pool             *ip_manager.IPPool
}

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

func NewYoutubeVideo(directory string, videoData *ytdl.YtdlVideo, playlistPosition int64, awsConfig aws.Config, stopGroup *stop.Group, pool *ip_manager.IPPool) (*YoutubeVideo, error) {
	// youtube-dl returns times in local timezone sometimes. this could break in the future
	// maybe we can file a PR to choose the timezone we want from youtube-dl
	publishedAt, err := time.ParseInLocation("20060102", videoData.UploadDate, time.Local)
	if err != nil {
		return nil, errors.Err(err)
	}
	return &YoutubeVideo{
		id:               videoData.ID,
		title:            videoData.Title,
		description:      videoData.Description,
		playlistPosition: playlistPosition,
		publishedAt:      publishedAt,
		dir:              directory,
		youtubeInfo:      videoData,
		awsConfig:        awsConfig,
		mocked:           false,
		youtubeChannelID: videoData.ChannelID,
		stopGroup:        stopGroup,
		pool:             pool,
	}, nil
}
func NewMockedVideo(directory string, videoID string, youtubeChannelID string, awsConfig aws.Config, stopGroup *stop.Group, pool *ip_manager.IPPool) *YoutubeVideo {
	return &YoutubeVideo{
		id:               videoID,
		playlistPosition: 0,
		dir:              directory,
		awsConfig:        awsConfig,
		mocked:           true,
		youtubeChannelID: youtubeChannelID,
		stopGroup:        stopGroup,
		pool:             pool,
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
	maxLength := 2800
	description := strings.TrimSpace(v.description)
	additionalDescription := "\nhttps://www.youtube.com/watch?v=" + v.id
	khanAcademyClaimID := "5fc52291980268b82413ca4c0ace1b8d749f3ffb"
	if v.lbryChannelID == khanAcademyClaimID {
		additionalDescription = additionalDescription + "\nNote: All Khan Academy content is available for free at (www.khanacademy.org)"
	}
	if len(description) > maxLength {
		description = description[:maxLength]
	}
	return description + "\n..." + additionalDescription
}

func (v *YoutubeVideo) download() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("download").Add(time.Since(start))
	}(start)
	if v.youtubeInfo.IsLive != nil {
		return errors.Err("video is a live stream and hasn't completed yet")
	}
	videoPath := v.getFullPath()

	err := os.Mkdir(v.videoDir(), 0777)
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
	qualities := []string{
		"1080",
		"720",
		"480",
		"320",
	}
	ytdlArgs := []string{
		"--no-progress",
		"-o" + strings.TrimSuffix(v.getFullPath(), ".mp4"),
		"--merge-output-format",
		"mp4",
		"--rm-cache-dir",
		"--postprocessor-args",
		"-movflags faststart",
		"--abort-on-unavailable-fragment",
		"--fragment-retries",
		"0",
		"--user-agent",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.88 Safari/537.36",
		"--cookies",
		"cookies.txt",
	}
	if v.maxVideoSize > 0 {
		ytdlArgs = append(ytdlArgs,
			"--max-filesize",
			fmt.Sprintf("%dM", v.maxVideoSize),
		)
	}
	if v.maxVideoLength > 0 {
		ytdlArgs = append(ytdlArgs,
			"--match-filter",
			fmt.Sprintf("duration <= %d", int(v.maxVideoLength.Seconds())),
		)
	}

	var sourceAddress string
	for {
		sourceAddress, err = v.pool.GetIP(v.id)
		if err != nil {
			if errors.Is(err, ip_manager.ErrAllThrottled) {
				select {
				case <-v.stopGroup.Ch():
					return errors.Err("interrupted by user")
				default:
					time.Sleep(ip_manager.IPCooldownPeriod)
					continue
				}
			} else {
				return err
			}
		}
		break
	}
	defer v.pool.ReleaseIP(sourceAddress)

	ytdlArgs = append(ytdlArgs,
		"--source-address",
		sourceAddress,
		"https://www.youtube.com/watch?v="+v.ID(),
	)

	for i, quality := range qualities {
		argsWithFilters := append(ytdlArgs, "-fbestvideo[ext=mp4][height<="+quality+"]+bestaudio[ext!=webm]")
		cmd := exec.Command("youtube-dl", argsWithFilters...)
		log.Printf("Running command youtube-dl %s", strings.Join(argsWithFilters, " "))

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
		err = cmd.Wait()
		if err != nil {
			if strings.Contains(err.Error(), "exit status 1") {
				if strings.Contains(string(errorLog), "HTTP Error 429") || strings.Contains(string(errorLog), "returned non-zero exit status 8") {
					v.pool.SetThrottled(sourceAddress)
				} else if strings.Contains(string(errorLog), "giving up after 0 fragment retries") {
					if i == (len(qualities) - 1) {
						return errors.Err(string(errorLog))
					}
					continue //this bypasses the yt throttling IP redistribution... TODO: don't
				}
				return errors.Err(string(errorLog))
			}
			return errors.Err(err)
		}
		log.Debugln(string(outLog))

		if strings.Contains(string(outLog), "does not pass filter duration") {
			_ = v.delete("does not pass filter duration")
			return errors.Err("video is too long to process")
		}
		if strings.Contains(string(outLog), "File is larger than max-filesize") {
			_ = v.delete("File is larger than max-filesize")
			return errors.Err("the video is too big to sync, skipping for now")
		}
		if string(errorLog) != "" {
			log.Printf("Command finished with error: %v", errors.Err(string(errorLog)))
			_ = v.delete("due to error")
			return errors.Err(string(errorLog))
		}
		fi, err := os.Stat(v.getFullPath())
		if err != nil {
			return errors.Err(err)
		}
		err = os.Chmod(v.getFullPath(), 0777)
		if err != nil {
			return errors.Err(err)
		}
		videoSize := fi.Size()
		v.size = &videoSize
		break
	}
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
func (v *YoutubeVideo) delete(reason string) error {
	videoPath, err := v.getDownloadedPath()
	if err != nil {
		log.Errorln(err)
		return err
	}
	err = os.Remove(videoPath)
	log.Debugf("%s deleted from disk for '%s' (%s)", v.id, reason, videoPath)

	if err != nil {
		err = errors.Prefix("delete error", err)
		log.Errorln(err)
		return err
	}

	return nil
}

func (v *YoutubeVideo) triggerThumbnailSave() (err error) {
	thumbnail := thumbs.GetBestThumbnail(v.youtubeInfo.Thumbnails)
	v.thumbnailURL, err = thumbs.MirrorThumbnail(thumbnail.URL, v.ID(), v.awsConfig)
	return err
}

func (v *YoutubeVideo) publish(daemon *jsonrpc.Client, params SyncParams) (*SyncSummary, error) {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("publish").Add(time.Since(start))
	}(start)
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
			FundingAccountIDs: []string{
				params.DefaultAccount,
			},
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
	MaxVideoLength time.Duration
	Fee            *sdk.Fee
	DefaultAccount string
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

func (v *YoutubeVideo) downloadAndPublish(daemon *jsonrpc.Client, params SyncParams) (*SyncSummary, error) {
	var err error

	dur := time.Duration(v.youtubeInfo.Duration) * time.Second

	if dur > v.maxVideoLength {
		log.Infof("%s is %d long and the limit is %s", v.id, dur.String(), v.maxVideoLength.String())
		logUtils.SendErrorToSlack("%s is %d long and the limit is %s", v.id, dur.String(), v.maxVideoLength.String())
		return nil, errors.Err("video is too long to process")
	}
	for {
		err = v.download()
		if err != nil && strings.Contains(err.Error(), "HTTP Error 429") {
			continue
		} else if err != nil {
			return nil, errors.Prefix("download error", err)
		}
		break
	}

	log.Debugln("Downloaded " + v.id)

	err = v.triggerThumbnailSave()
	if err != nil {
		return nil, errors.Prefix("thumbnail error", err)
	}
	log.Debugln("Created thumbnail for " + v.id)

	summary, err := v.publish(daemon, params)
	//delete the video in all cases (and ignore the error)
	_ = v.delete("finished download and publish")

	return summary, errors.Prefix("publish error", err)
}

func (v *YoutubeVideo) getMetadata() (languages []string, locations []jsonrpc.Location, tags []string) {
	languages = nil
	locations = nil
	tags = nil
	if !v.mocked {
		/*
			if v.youtubeInfo.Snippet.DefaultLanguage != "" {
				if v.youtubeInfo.Snippet.DefaultLanguage == "iw" {
					v.youtubeInfo.Snippet.DefaultLanguage = "he"
				}
				languages = []string{v.youtubeInfo.Snippet.DefaultLanguage}
			}*/

		/*if v.youtubeInfo.!= nil && v.youtubeInfo.RecordingDetails.Location != nil {
			locations = []jsonrpc.Location{{
				Latitude:  util.PtrToString(fmt.Sprintf("%.7f", v.youtubeInfo.RecordingDetails.Location.Latitude)),
				Longitude: util.PtrToString(fmt.Sprintf("%.7f", v.youtubeInfo.RecordingDetails.Location.Longitude)),
			}}
		}*/
		tags = v.youtubeInfo.Tags
	}
	tags, err := tags_manager.SanitizeTags(tags, v.youtubeChannelID)
	if err != nil {
		log.Errorln(err.Error())
	}
	if !v.mocked {
		for _, category := range v.youtubeInfo.Categories {
			tags = append(tags, youtubeCategories[category])
		}
	}

	return languages, locations, tags
}

func (v *YoutubeVideo) reprocess(daemon *jsonrpc.Client, params SyncParams, existingVideoData *sdk.SyncedVideo) (*SyncSummary, error) {
	c, err := daemon.ClaimSearch(nil, &existingVideoData.ClaimID, nil, nil, 1, 20)
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
		thumbnail := thumbs.GetBestThumbnail(v.youtubeInfo.Thumbnails)
		thumbnailURL, err = thumbs.MirrorThumbnail(thumbnail.URL, v.ID(), v.awsConfig)
	} else {
		thumbnailURL = thumbs.ThumbnailEndpoint + v.ID()
	}

	videoSize, err := currentClaim.GetStreamSizeByMagic()
	if err != nil {
		if existingVideoData.Size > 0 {
			videoSize = uint64(existingVideoData.Size)
		} else {
			log.Infof("%s: the video must be republished as we can't get the right size", v.ID())
			if !v.mocked {
				_, err = daemon.StreamAbandon(currentClaim.Txid, currentClaim.Nout, nil, true)
				if err != nil {
					return nil, errors.Err(err)
				}
				return v.downloadAndPublish(daemon, params)

			}
			return nil, errors.Err("the video must be republished as we can't get the right size but it doesn't exist on youtube anymore")
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
			FundingAccountIDs: []string{
				params.DefaultAccount,
			},
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
		start := time.Now()
		pr, err := daemon.StreamUpdate(existingVideoData.ClaimID, jsonrpc.StreamUpdateOptions{
			StreamCreateOptions: streamCreateOptions,
			FileSize:            &videoSize,
		})
		timing.TimedComponent("StreamUpdate").Add(time.Since(start))
		if err != nil {
			return nil, err
		}

		return &SyncSummary{
			ClaimID:   pr.Outputs[0].ClaimID,
			ClaimName: pr.Outputs[0].Name,
		}, nil
	}

	streamCreateOptions.ClaimCreateOptions.Title = &v.title
	streamCreateOptions.ClaimCreateOptions.Description = util.PtrToString(v.getAbbrevDescription())
	streamCreateOptions.Duration = util.PtrToUint64(uint64(v.youtubeInfo.Duration))
	streamCreateOptions.ReleaseTime = util.PtrToInt64(v.publishedAt.Unix())
	start := time.Now()
	pr, err := daemon.StreamUpdate(existingVideoData.ClaimID, jsonrpc.StreamUpdateOptions{
		ClearLanguages:      util.PtrToBool(true),
		ClearLocations:      util.PtrToBool(true),
		ClearTags:           util.PtrToBool(true),
		StreamCreateOptions: streamCreateOptions,
		FileSize:            &videoSize,
	})
	timing.TimedComponent("StreamUpdate").Add(time.Since(start))
	if err != nil {
		return nil, err
	}

	return &SyncSummary{
		ClaimID:   pr.Outputs[0].ClaimID,
		ClaimName: pr.Outputs[0].Name,
	}, nil
}
