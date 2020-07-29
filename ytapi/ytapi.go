package ytapi

import (
	"bufio"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lbryio/ytsync/v5/downloader/ytdl"

	"github.com/lbryio/ytsync/v5/downloader"
	"github.com/lbryio/ytsync/v5/ip_manager"
	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/lbryio/ytsync/v5/sources"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/stop"

	"github.com/aws/aws-sdk-go/aws"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	ytlib "google.golang.org/api/youtube/v3"
)

type Video interface {
	Size() *int64
	ID() string
	IDAndNum() string
	PlaylistPosition() int
	PublishedAt() time.Time
	Sync(*jsonrpc.Client, sources.SyncParams, *sdk.SyncedVideo, bool, *sync.RWMutex) (*sources.SyncSummary, error)
}

type byPublishedAt []Video

func (a byPublishedAt) Len() int           { return len(a) }
func (a byPublishedAt) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byPublishedAt) Less(i, j int) bool { return a[i].PublishedAt().Before(a[j].PublishedAt()) }

type VideoParams struct {
	VideoDir string
	S3Config aws.Config
	Stopper  *stop.Group
	IPPool   *ip_manager.IPPool
}

var mostRecentlyFailedChannel string // TODO: fix this hack!

func GetVideosToSync(channelID string, syncedVideos map[string]sdk.SyncedVideo, quickSync bool, maxVideos int, videoParams VideoParams) ([]Video, error) {

	var videos []Video
	if quickSync {
		maxVideos = 50
	}
	videoIDs, err := downloader.GetPlaylistVideoIDs(channelID, maxVideos, videoParams.Stopper.Ch())
	if err != nil {
		return nil, errors.Err(err)
	}

	log.Infof("Got info for %d videos from youtube downloader", len(videoIDs))

	playlistMap := make(map[string]int64)
	for i, videoID := range videoIDs {
		playlistMap[videoID] = int64(i)
	}

	if len(videoIDs) < 1 {
		if channelID == mostRecentlyFailedChannel {
			return nil, errors.Err("playlist items not found")
		}
		mostRecentlyFailedChannel = channelID
	}

	vids, err := getVideos(videoIDs, videoParams.Stopper.Ch(), videoParams.IPPool)
	if err != nil {
		return nil, err
	}

	for _, item := range vids {
		positionInList := playlistMap[item.ID]
		videoToAdd, err := sources.NewYoutubeVideo(videoParams.VideoDir, item, positionInList, videoParams.S3Config, videoParams.Stopper, videoParams.IPPool)
		if err != nil {
			return nil, errors.Err(err)
		}
		videos = append(videos, videoToAdd)
	}

	for k, v := range syncedVideos {
		if !v.Published {
			continue
		}
		if _, ok := playlistMap[k]; !ok {
			videos = append(videos, sources.NewMockedVideo(videoParams.VideoDir, k, channelID, videoParams.S3Config, videoParams.Stopper, videoParams.IPPool))
		}
	}

	sort.Sort(byPublishedAt(videos))

	return videos, nil
}

func CountVideosInChannel(channelID string) (int, error) {
	res, err := http.Get("https://socialblade.com/youtube/channel/" + channelID)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	var line string

	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "youtube-stats-header-uploads") {
			line = scanner.Text()
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if line == "" {
		return 0, errors.Err("upload count line not found")
	}

	matches := regexp.MustCompile(">([0-9]+)<").FindStringSubmatch(line)
	if len(matches) != 2 {
		return 0, errors.Err("upload count not found with regex")
	}

	num, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, errors.Err(err)
	}

	return num, nil
}

func ChannelInfo(apiKey, channelID string) (*ytlib.ChannelSnippet, *ytlib.ChannelBrandingSettings, error) {
	return nil, nil, errors.Err("ChannelInfo doesn't work yet because we're focused on existing channels")

	service, err := ytlib.New(&http.Client{Transport: &transport.APIKey{Key: apiKey}})
	if err != nil {
		return nil, nil, errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Channels.List("snippet,brandingSettings").Id(channelID).Do()
	if err != nil {
		return nil, nil, errors.Prefix("error getting channel details", err)
	}

	if len(response.Items) < 1 {
		return nil, nil, errors.Err("youtube channel not found")
	}

	return response.Items[0].Snippet, response.Items[0].BrandingSettings, nil
}

func getVideos(videoIDs []string, stopChan stop.Chan, ipPool *ip_manager.IPPool) ([]*ytdl.YtdlVideo, error) {
	var videos []*ytdl.YtdlVideo
	for _, videoID := range videoIDs {
		select {
		case <-stopChan:
			return videos, errors.Err("canceled by stopper")
		default:
		}

		//ip, err := ipPool.GetIP(videoID)
		//if err != nil {
		//	return nil, err
		//}
		//video, err := downloader.GetVideoInformation(videoID, &net.TCPAddr{IP: net.ParseIP(ip)})
		video, err := downloader.GetVideoInformation(videoID, stopChan, nil)
		if err != nil {
			//ipPool.ReleaseIP(ip)
			return nil, errors.Err(err)
		}
		videos = append(videos, video)
		//ipPool.ReleaseIP(ip)
	}
	return videos, nil
}
