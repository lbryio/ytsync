package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/abadojack/whatlanggo"
	"github.com/lbryio/ytsync/v5/downloader"
	"github.com/lbryio/ytsync/v5/downloader/ytdl"
	"github.com/lbryio/ytsync/v5/shared"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
	"gopkg.in/vansante/go-ffprobe.v2"

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
	thumbnailURL     string
	lbryChannelID    string
	mocked           bool
	walletLock       *sync.RWMutex
	stopGroup        *stop.Group
	pool             *ip_manager.IPPool
	progressBars     *mpb.Progress
	progressBarWg    *sync.WaitGroup
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

func NewYoutubeVideo(directory string, videoData *ytdl.YtdlVideo, playlistPosition int64, stopGroup *stop.Group, pool *ip_manager.IPPool) (*YoutubeVideo, error) {
	// youtube-dl returns times in local timezone sometimes. this could break in the future
	// maybe we can file a PR to choose the timezone we want from youtube-dl
	return &YoutubeVideo{
		id:               videoData.ID,
		title:            videoData.Title,
		description:      videoData.Description,
		playlistPosition: playlistPosition,
		publishedAt:      videoData.UploadDateForReal,
		dir:              directory,
		youtubeInfo:      videoData,
		mocked:           false,
		youtubeChannelID: videoData.ChannelID,
		stopGroup:        stopGroup,
		pool:             pool,
	}, nil
}

func NewMockedVideo(directory string, videoID string, youtubeChannelID string, stopGroup *stop.Group, pool *ip_manager.IPPool) *YoutubeVideo {
	return &YoutubeVideo{
		id:               videoID,
		playlistPosition: 0,
		dir:              directory,
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
func checkCookiesIntegrity() error {
	fi, err := os.Stat("cookies.txt")
	if err != nil {
		return errors.Err(err)
	}
	if fi.Size() == 0 {
		log.Errorf("cookies were cleared out. Attempting a restore from cookies-backup.txt")
		input, err := ioutil.ReadFile("cookies-backup.txt")
		if err != nil {
			return errors.Err(err)
		}

		err = ioutil.WriteFile("cookies.txt", input, 0644)
		if err != nil {
			return errors.Err(err)
		}
	}
	return nil
}

func (v *YoutubeVideo) download() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("download").Add(time.Since(start))
	}(start)

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
		"360",
	}
	dur := time.Duration(v.youtubeInfo.Duration) * time.Second
	if dur.Hours() > 1 { //for videos longer than 1 hour only sync up to 720p
		qualities = []string{
			"720",
			"480",
			"360",
		}
	}

	metadataPath := path.Join(logUtils.GetVideoMetadataDir(), v.id+".info.json")
	_, err = os.Stat(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.Err("metadata information for video %s is missing! Why?", v.id)
		}
		return errors.Err(err)
	}

	metadata, err := parseVideoMetadata(metadataPath)

	err = checkCookiesIntegrity()
	if err != nil {
		return err
	}

	ytdlArgs := []string{
		"--no-progress",
		"-o" + strings.TrimSuffix(v.getFullPath(), ".mp4"),
		"--merge-output-format",
		"mp4",
		"--postprocessor-args",
		"ffmpeg:-movflags faststart",
		"--abort-on-unavailable-fragment",
		"--fragment-retries",
		"1",
		"--cookies",
		"cookies.txt",
		"--extractor-args",
		"youtube:player_client=android",
		//"--concurrent-fragments",
		//"2",
		"--load-info-json",
		metadataPath,
	}

	userAgent := []string{"--user-agent", downloader.ChromeUA}
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
		fmt.Sprintf("https://www.youtube.com/watch?v=%s", v.id),
	)
	//speedThrottleRetries := 3
	for i := 0; i < len(qualities); i++ {
		quality := qualities[i]
		argsWithFilters := append(ytdlArgs, "-fbestvideo[ext=mp4][vcodec!*=av01][height<="+quality+"]+bestaudio[ext!=webm][format_id!=258][format_id!=251][format_id!=256][format_id!=327]")
		argsWithFilters = append(argsWithFilters, userAgent...)
		//if speedThrottleRetries > 0 {
		//	speedThrottleRetries--
		//	argsWithFilters = append(argsWithFilters, "--throttled-rate", "180K")
		//}
		cmd := exec.Command("yt-dlp", argsWithFilters...)
		log.Printf("Running command yt-dlp %s", strings.Join(argsWithFilters, " "))

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

		dlStopGrp := stop.New()

		ticker := time.NewTicker(400 * time.Millisecond)
		go v.trackProgressBar(argsWithFilters, ticker, metadata, dlStopGrp, sourceAddress)

		//ticker2 := time.NewTicker(10 * time.Second)
		//v.monitorSlowDownload(ticker, dlStopGrp, sourceAddress, cmd)

		errorLog, _ := ioutil.ReadAll(stderr)
		outLog, _ := ioutil.ReadAll(stdout)
		err = cmd.Wait()

		//stop the progress bar
		ticker.Stop()
		dlStopGrp.Stop()

		if err != nil {
			if strings.Contains(err.Error(), "exit status 1") {
				if strings.Contains(string(errorLog), "HTTP Error 429") || strings.Contains(string(errorLog), "returned non-zero exit status 8") {
					v.pool.SetThrottled(sourceAddress)
				} else if strings.Contains(string(errorLog), "giving up after 0 fragment retries") {
					if i == (len(qualities) - 1) {
						return errors.Err(string(errorLog))
					}
					continue //this bypasses the yt throttling IP redistribution... TODO: don't
				} else if strings.Contains(string(errorLog), "YouTube said: Unable to extract video data") && !strings.Contains(userAgent[1], "Googlebot") {
					i-- //do not lower quality when trying a different user agent
					userAgent = []string{downloader.GoogleBotUA}
					log.Infof("trying different user agent for video %s", v.ID())
					continue
					//} else if strings.Contains(string(errorLog), "yt_dlp.utils.ThrottledDownload") {
					//	log.Infof("throttled download speed for video %s. Retrying", v.ID())
					//	i-- //do not lower quality when we're retrying a throttled download
					//	continue
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
			if strings.Contains(string(errorLog), "HTTP Error 429") {
				v.pool.SetThrottled(sourceAddress)
			}
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
func (v *YoutubeVideo) monitorSlowDownload(ticker *time.Ticker, stop *stop.Group, address string, cmd *exec.Cmd) {
	count := 0
	lastSize := int64(0)
	for {
		select {
		case <-stop.Ch():
			return
		case <-ticker.C:
			size, err := logUtils.DirSize(v.videoDir())
			if err != nil {
				log.Errorf("error while getting size of download directory: %s", errors.FullTrace(err))
				continue
			}
			delta := size - lastSize
			avgSpeed := delta / 10
			if avgSpeed < 200*1024 { //200 KB/s
				count++
			} else {
				count--
			}
			if count > 3 {
				err := cmd.Process.Signal(syscall.SIGKILL)
				if err != nil {
					log.Errorf("failure in killing slow download: %s", errors.Err(err))
					return
				}
			}
		}
	}
}

func (v *YoutubeVideo) trackProgressBar(argsWithFilters []string, ticker *time.Ticker, metadata *ytMetadata, done *stop.Group, sourceAddress string) {
	v.progressBarWg.Add(1)
	go func() {
		defer v.progressBarWg.Done()
		//get size of the video before downloading
		cmd := exec.Command("yt-dlp", append(argsWithFilters, "-s")...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Errorf("error while getting final file size: %s", errors.FullTrace(err))
			return
		}

		if err := cmd.Start(); err != nil {
			log.Errorf("error while getting final file size: %s", errors.FullTrace(err))
			return
		}
		outLog, _ := ioutil.ReadAll(stdout)
		err = cmd.Wait()
		output := string(outLog)
		parts := strings.Split(output, ": ")
		if len(parts) != 3 {
			log.Errorf("couldn't parse audio and video parts from the output (%s)", output)
			return
		}
		formats := strings.Split(parts[2], "+")
		if len(formats) != 2 {
			log.Errorf("couldn't parse formats from the output (%s)", output)
			return
		}
		log.Debugf("'%s'", output)
		videoFormat := formats[0]
		audioFormat := strings.Replace(formats[1], "\n", "", -1)

		videoSize := 0
		audioSize := 0
		if metadata != nil {
			for _, f := range metadata.Formats {
				if f.FormatID == videoFormat {
					videoSize = f.Filesize
				}
				if f.FormatID == audioFormat {
					audioSize = f.Filesize
				}
			}
		}

		log.Debugf("(%s) - videoSize: %d (%s), audiosize: %d (%s)", v.id, videoSize, videoFormat, audioSize, audioFormat)
		bar := v.progressBars.AddBar(int64(videoSize+audioSize),
			mpb.PrependDecorators(
				decor.CountersKibiByte("% .2f / % .2f "),
				// simple name decorator
				decor.Name(fmt.Sprintf("id: %s src-ip: (%s)", v.id, sourceAddress)),
				// decor.DSyncWidth bit enables column width synchronization
				decor.Percentage(decor.WCSyncSpace),
			),
			mpb.AppendDecorators(
				decor.EwmaETA(decor.ET_STYLE_GO, 90),
				decor.Name(" ] "),
				decor.EwmaSpeed(decor.UnitKiB, "% .2f ", 60),
				decor.OnComplete(
					// ETA decorator with ewma age of 60
					decor.EwmaETA(decor.ET_STYLE_GO, 60), "done",
				),
			),
			mpb.BarRemoveOnComplete(),
		)
		defer func() {
			bar.Completed()
			bar.Abort(true)
		}()
		for {
			select {
			case <-done.Ch():
				return
			case <-ticker.C:
				size, err := logUtils.DirSize(v.videoDir())
				if err != nil {
					log.Errorf("error while getting size of download directory: %s", errors.FullTrace(err))
					return
				}
				bar.SetCurrent(size)
				if size > int64(videoSize+audioSize) {
					bar.SetTotal(size+2048, false)
				}
				bar.DecoratorEwmaUpdate(400 * time.Millisecond)
			}
		}
	}()
}

type ytMetadata struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Formats []struct {
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
	} `json:"formats"`
	Thumbnails []struct {
		Height     int    `json:"height"`
		URL        string `json:"url"`
		Width      int    `json:"width"`
		Resolution string `json:"resolution"`
		ID         string `json:"id"`
	} `json:"thumbnails"`
	Description        string        `json:"description"`
	UploadDate         string        `json:"upload_date"`
	Uploader           string        `json:"uploader"`
	UploaderID         string        `json:"uploader_id"`
	UploaderURL        string        `json:"uploader_url"`
	ChannelID          string        `json:"channel_id"`
	ChannelURL         string        `json:"channel_url"`
	Duration           int           `json:"duration"`
	ViewCount          int           `json:"view_count"`
	AverageRating      float64       `json:"average_rating"`
	AgeLimit           int           `json:"age_limit"`
	WebpageURL         string        `json:"webpage_url"`
	Categories         []string      `json:"categories"`
	Tags               []interface{} `json:"tags"`
	IsLive             interface{}   `json:"is_live"`
	LikeCount          int           `json:"like_count"`
	DislikeCount       int           `json:"dislike_count"`
	Channel            string        `json:"channel"`
	Extractor          string        `json:"extractor"`
	WebpageURLBasename string        `json:"webpage_url_basename"`
	ExtractorKey       string        `json:"extractor_key"`
	Playlist           interface{}   `json:"playlist"`
	PlaylistIndex      interface{}   `json:"playlist_index"`
	Thumbnail          string        `json:"thumbnail"`
	DisplayID          string        `json:"display_id"`
	Format             string        `json:"format"`
	FormatID           string        `json:"format_id"`
	Width              int           `json:"width"`
	Height             int           `json:"height"`
	Resolution         interface{}   `json:"resolution"`
	Fps                int           `json:"fps"`
	Vcodec             string        `json:"vcodec"`
	Vbr                float64       `json:"vbr"`
	StretchedRatio     interface{}   `json:"stretched_ratio"`
	Acodec             string        `json:"acodec"`
	Abr                float64       `json:"abr"`
	Ext                string        `json:"ext"`
	Fulltitle          string        `json:"fulltitle"`
	Filename           string        `json:"_filename"`
}

func parseVideoMetadata(metadataPath string) (*ytMetadata, error) {
	f, err := os.Open(metadataPath)
	if err != nil {
		return nil, errors.Err(err)
	}
	// defer the closing of our jsonFile so that we can parse it later on
	defer f.Close()
	// read our opened jsonFile as a byte array.
	byteValue, _ := ioutil.ReadAll(f)

	// we initialize our Users array
	var m ytMetadata

	// we unmarshal our byteArray which contains our
	// jsonFile's content into 'users' which we defined above
	err = json.Unmarshal(byteValue, &m)
	if err != nil {
		return nil, errors.Err(err)
	}
	return &m, nil
}

func (v *YoutubeVideo) videoDir() string {
	return path.Join(v.dir, v.id)
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
			return path.Join(v.videoDir(), f.Name()), nil
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
	if thumbnail.Width == 0 {
		return errors.Err("default youtube thumbnail found")
	}
	v.thumbnailURL, err = thumbs.MirrorThumbnail(thumbnail.URL, v.ID())
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
	urlsRegex := regexp.MustCompile(`(?m) ?(f|ht)(tp)(s?)(://)(.*)[.|/](.*)`)
	descriptionSample := urlsRegex.ReplaceAllString(v.description, "")
	info := whatlanggo.Detect(descriptionSample)
	info2 := whatlanggo.Detect(v.title)
	if info.IsReliable() && info.Lang.Iso6391() != "" {
		language := info.Lang.Iso6391()
		languages = []string{language}
	} else if info2.IsReliable() && info2.Lang.Iso6391() != "" {
		language := info2.Lang.Iso6391()
		languages = []string{language}
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
	Fee            *shared.Fee
	DefaultAccount string
}

func (v *YoutubeVideo) Sync(daemon *jsonrpc.Client, params SyncParams, existingVideoData *sdk.SyncedVideo, reprocess bool, walletLock *sync.RWMutex, pbWg *sync.WaitGroup, pb *mpb.Progress) (*SyncSummary, error) {
	v.maxVideoSize = int64(params.MaxVideoSize)
	v.maxVideoLength = params.MaxVideoLength
	v.lbryChannelID = params.ChannelID
	v.walletLock = walletLock
	v.progressBars = pb
	v.progressBarWg = pbWg
	if reprocess && existingVideoData != nil && existingVideoData.Published {
		summary, err := v.reprocess(daemon, params, existingVideoData)
		return summary, errors.Prefix("upgrade failed", err)
	}
	return v.downloadAndPublish(daemon, params)
}

func (v *YoutubeVideo) downloadAndPublish(daemon *jsonrpc.Client, params SyncParams) (*SyncSummary, error) {
	var err error

	dur := time.Duration(v.youtubeInfo.Duration) * time.Second
	minDuration := 7 * time.Second

	if v.youtubeInfo.IsLive == true {
		return nil, errors.Err("video is a live stream and hasn't completed yet")
	}
	if dur > v.maxVideoLength {
		logUtils.SendErrorToSlack("%s is %s long and the limit is %s", v.id, dur.String(), v.maxVideoLength.String())
		return nil, errors.Err("video is too long to process")
	}
	if dur < minDuration {
		logUtils.SendErrorToSlack("%s is %s long and the minimum is %s", v.id, dur.String(), minDuration.String())
		return nil, errors.Err("video is too short to process")
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

	ctx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFn()

	data, err := ffprobe.ProbeURL(ctx, v.getFullPath())
	if err != nil {
		log.Errorf("failure in probing downloaded video: %s", err.Error())
	} else {
		if data.Format.Duration() < minDuration {
			return nil, errors.Err("video is too short to process")
		}
	}
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
		thumbnailURL, err = thumbs.MirrorThumbnail(thumbnail.URL, v.ID())
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
			return nil, errors.Prefix("the video must be republished as we can't get the right size and it doesn't exist on youtube anymore", err)
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
