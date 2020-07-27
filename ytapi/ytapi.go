package ytapi

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

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
	Grp      *stop.Group
	IPPool   *ip_manager.IPPool
}

var mostRecentlyFailedChannel string // TODO: fix this hack!

func GetVideosToSync(apiKey, channelID string, syncedVideos map[string]sdk.SyncedVideo, quickSync bool, maxVideos int, videoParams VideoParams) ([]Video, error) {
	playlistID, err := getPlaylistID(apiKey, channelID)
	if err != nil {
		return nil, err
	}

	playlistMap := make(map[string]*ytlib.PlaylistItemSnippet, 50)
	var playlistItems []*ytlib.PlaylistItem
	var nextPageToken string
	var videos []Video

	for {
		playlistItems, nextPageToken, err = getPlaylistItems(apiKey, playlistID, nextPageToken)

		if len(playlistItems) < 1 {
			// If there are 50+ videos in a playlist but less than 50 are actually returned by the API, youtube will still redirect
			// clients to a next page. Such next page will however be empty. This logic prevents ytsync from failing.
			youtubeIsLying := len(videos) > 0
			if youtubeIsLying {
				break
			}
			if channelID == mostRecentlyFailedChannel {
				return nil, errors.Err("playlist items not found")
			}
			mostRecentlyFailedChannel = channelID
			break //return errors.Err("playlist items not found") //TODO: will this work?
		}

		videoIDs := make([]string, len(playlistItems))
		for i, item := range playlistItems {
			// normally we'd send the video into the channel here, but youtube api doesn't have sorting
			// so we have to get ALL the videos, then sort them, then send them in
			playlistMap[item.Snippet.ResourceId.VideoId] = item.Snippet
			videoIDs[i] = item.Snippet.ResourceId.VideoId
		}

		vids, err := getVideos(apiKey, videoIDs)
		if err != nil {
			return nil, err
		}

		for _, item := range vids {
			videos = append(videos, sources.NewYoutubeVideo(videoParams.VideoDir, item, playlistMap[item.Id].Position, videoParams.S3Config, videoParams.Grp, videoParams.IPPool))
		}

		log.Infof("Got info for %d videos from youtube API", len(videos))

		if nextPageToken == "" || quickSync || len(videos) >= maxVideos {
			break
		}
	}

	for k, v := range syncedVideos {
		if !v.Published {
			continue
		}
		if _, ok := playlistMap[k]; !ok {
			videos = append(videos, sources.NewMockedVideo(videoParams.VideoDir, k, channelID, videoParams.S3Config, videoParams.Grp, videoParams.IPPool))
		}

	}

	sort.Sort(byPublishedAt(videos))

	return videos, nil
}

func CountVideosInChannel(apiKey, channelID string) (uint64, error) {
	client := &http.Client{
		Transport: &transport.APIKey{Key: apiKey},
	}

	service, err := ytlib.New(client)
	if err != nil {
		return 0, errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Channels.List("statistics").Id(channelID).Do()
	if err != nil {
		return 0, errors.Prefix("error getting channels", err)
	}

	if len(response.Items) < 1 {
		return 0, errors.Err("youtube channel not found")
	}

	return response.Items[0].Statistics.VideoCount, nil
}

func ChannelInfo(apiKey, channelID string) (*ytlib.ChannelSnippet, *ytlib.ChannelBrandingSettings, error) {
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

func getPlaylistID(apiKey, channelID string) (string, error) {
	service, err := ytlib.New(&http.Client{Transport: &transport.APIKey{Key: apiKey}})
	if err != nil {
		return "", errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Channels.List("contentDetails").Id(channelID).Do()
	if err != nil {
		return "", errors.Prefix("error getting channel details", err)
	}

	if len(response.Items) < 1 {
		return "", errors.Err("youtube channel not found")
	}

	if response.Items[0].ContentDetails.RelatedPlaylists == nil {
		return "", errors.Err("no related playlists")
	}

	playlistID := response.Items[0].ContentDetails.RelatedPlaylists.Uploads
	if playlistID == "" {
		return "", errors.Err("no channel playlist")
	}

	return playlistID, nil
}

func getPlaylistItems(apiKey, playlistID, nextPageToken string) ([]*ytlib.PlaylistItem, string, error) {
	service, err := ytlib.New(&http.Client{Transport: &transport.APIKey{Key: apiKey}})
	if err != nil {
		return nil, "", errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.PlaylistItems.List("snippet").
		PlaylistId(playlistID).
		MaxResults(50).
		PageToken(nextPageToken).
		Do()

	if err != nil {
		return nil, "", errors.Prefix("error getting playlist items", err)
	}

	return response.Items, response.NextPageToken, nil
}

func getVideos(apiKey string, videoIDs []string) ([]*ytlib.Video, error) {
	service, err := ytlib.New(&http.Client{Transport: &transport.APIKey{Key: apiKey}})
	if err != nil {
		return nil, errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Videos.List("snippet,contentDetails,recordingDetails").Id(strings.Join(videoIDs[:], ",")).Do()
	if err != nil {
		return nil, errors.Prefix("error getting videos info", err)
	}

	return response.Items, nil
}
