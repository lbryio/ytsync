package ytsync

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lbryio/lbry.go/jsonrpc"
	"github.com/lbryio/lbry.go/stopOnce"
	"github.com/lbryio/lbry.go/ytsync/redisdb"
	"github.com/lbryio/lbry.go/ytsync/sources"

	"github.com/go-errors/errors"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

const (
	channelClaimAmount = 0.01
	publishAmount      = 0.01
)

type video interface {
	ID() string
	IDAndNum() string
	PublishedAt() time.Time
	Sync(*jsonrpc.Client, string, float64, string) error
}

// sorting videos
type byPublishedAt []video

func (a byPublishedAt) Len() int           { return len(a) }
func (a byPublishedAt) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byPublishedAt) Less(i, j int) bool { return a[i].PublishedAt().Before(a[j].PublishedAt()) }

// Sync stores the options that control how syncing happens
type Sync struct {
	YoutubeAPIKey           string
	YoutubeChannelID        string
	LbryChannelName         string
	StopOnError             bool
	MaxTries                int
	ConcurrentVideos        int
	TakeOverExistingChannel bool

	daemon         *jsonrpc.Client
	claimAddress   string
	videoDirectory string
	db             *redisdb.DB

	stop *stopOnce.Stopper

	wg    sync.WaitGroup
	queue chan video
}

func (s *Sync) FullCycle() error {
	var err error
	if os.Getenv("HOME") == "" {
		return errors.New("no $HOME env var found")
	}

	if s.YoutubeChannelID == "" {
		channelID, err := getChannelIDFromFile(s.LbryChannelName)
		if err != nil {
			return err
		}
		s.YoutubeChannelID = channelID
	}

	defaultWalletDir := os.Getenv("HOME") + "/.lbryum/wallets/default_wallet"
	walletBackupDir := os.Getenv("HOME") + "/wallets/" + strings.Replace(s.LbryChannelName, "@", "", 1)

	if _, err = os.Stat(walletBackupDir); !os.IsNotExist(err) {
		if _, err := os.Stat(defaultWalletDir); !os.IsNotExist(err) {
			return errors.New("Tried to continue previous upload, but default_wallet already exists")
		}

		err = os.Rename(walletBackupDir, defaultWalletDir)
		if err != nil {
			return errors.Wrap(err, 0)
		}
		log.Println("Continuing previous upload")
	}

	defer func() {
		log.Printf("Stopping daemon")
		shutdownErr := stopDaemonViaSystemd()
		if shutdownErr != nil {
			log.Errorf("error shutting down daemon: %v", shutdownErr)
			log.Errorf("WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR", shutdownErr)
		} else {
			walletErr := os.Rename(defaultWalletDir, walletBackupDir)
			if walletErr != nil {
				log.Errorf("error moving wallet to backup dir: %v", walletErr)
			}
		}
	}()

	s.videoDirectory, err = ioutil.TempDir("", "ytsync")
	if err != nil {
		return errors.Wrap(err, 0)
	}

	s.db = redisdb.New()
	s.stop = stopOnce.New()
	s.queue = make(chan video)

	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interruptChan
		log.Println("Got interrupt signal. Will shut down after current publishes finish")
		s.stop.Stop()
	}()

	log.Printf("Starting daemon")
	err = startDaemonViaSystemd()
	if err != nil {
		return err
	}

	log.Infoln("Waiting for daemon to finish starting...")
	s.daemon = jsonrpc.NewClientAndWait("")

	err = s.doSync()
	if err != nil {
		return err
	} else {
		// wait for reflection to finish???
		wait := 15 * time.Second // should bump this up to a few min, but keeping it low for testing
		log.Println("Waiting " + wait.String() + " to finish reflecting everything")
		time.Sleep(wait)
	}

	return nil
}

func (s *Sync) doSync() error {
	var err error

	err = s.walletSetup()
	if err != nil {
		return err
	}

	if s.StopOnError {
		log.Println("Will stop publishing if an error is detected")
	}

	for i := 0; i < s.ConcurrentVideos; i++ {
		go s.startWorker(i)
	}

	err = s.enqueueVideos()
	close(s.queue)
	s.wg.Wait()
	return err
}

func (s *Sync) startWorker(workerNum int) {
	s.wg.Add(1)
	defer s.wg.Done()

	var v video
	var more bool

	for {
		select {
		case <-s.stop.Chan():
			log.Printf("Stopping worker %d", workerNum)
			return
		default:
		}

		select {
		case v, more = <-s.queue:
			if !more {
				return
			}
		case <-s.stop.Chan():
			log.Printf("Stopping worker %d", workerNum)
			return
		}

		log.Println("========================================")

		tryCount := 0
		for {
			tryCount++
			err := s.processVideo(v)

			if err != nil {
				log.Errorln("error processing video: " + err.Error())
				if s.StopOnError {
					s.stop.Stop()
				} else if s.MaxTries > 1 {
					if strings.Contains(err.Error(), "non 200 status code received") ||
						strings.Contains(err.Error(), " reason: 'This video contains content from") ||
						strings.Contains(err.Error(), "dont know which claim to update") ||
						strings.Contains(err.Error(), "uploader has not made this video available in your country") ||
						strings.Contains(err.Error(), "Playback on other websites has been disabled by the video owner") {
						log.Println("This error should not be retried at all")
					} else if tryCount >= s.MaxTries {
						log.Printf("Video failed after %d retries, exiting", s.MaxTries)
						s.stop.Stop()
					} else {
						log.Println("Retrying")
						continue
					}
				}
			}
			break
		}
	}
}

func (s *Sync) enqueueVideos() error {
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return errors.WrapPrefix(err, "error creating YouTube service", 0)
	}

	response, err := service.Channels.List("contentDetails").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return errors.WrapPrefix(err, "error getting channels", 0)
	}

	if len(response.Items) < 1 {
		return errors.New("youtube channel not found")
	}

	if response.Items[0].ContentDetails.RelatedPlaylists == nil {
		return errors.New("no related playlists")
	}

	playlistID := response.Items[0].ContentDetails.RelatedPlaylists.Uploads
	if playlistID == "" {
		return errors.New("no channel playlist")
	}

	var videos []video

	nextPageToken := ""
	for {
		req := service.PlaylistItems.List("snippet").
			PlaylistId(playlistID).
			MaxResults(50).
			PageToken(nextPageToken)

		playlistResponse, err := req.Do()
		if err != nil {
			return errors.WrapPrefix(err, "error getting playlist items", 0)
		}

		if len(playlistResponse.Items) < 1 {
			return errors.New("playlist items not found")
		}

		for _, item := range playlistResponse.Items {
			// todo: there's thumbnail info here. why did we need lambda???

			// normally we'd send the video into the channel here, but youtube api doesn't have sorting
			// so we have to get ALL the videos, then sort them, then send them in
			videos = append(videos, sources.NewYoutubeVideo(s.videoDirectory, item.Snippet))
		}

		log.Infof("Got info for %d videos from youtube API", len(videos))

		nextPageToken = playlistResponse.NextPageToken
		if nextPageToken == "" {
			break
		}
	}

	sort.Sort(byPublishedAt(videos))
	//or sort.Sort(sort.Reverse(byPlaylistPosition(videos)))

Enqueue:
	for _, v := range videos {
		select {
		case <-s.stop.Chan():
			break Enqueue
		default:
		}

		select {
		case s.queue <- v:
		case <-s.stop.Chan():
			break Enqueue
		}
	}

	return nil
}

func (s *Sync) processVideo(v video) error {
	log.Println("Processing " + v.IDAndNum())
	defer func(start time.Time) {
		log.Println(v.ID() + " took " + time.Since(start).String())
	}(time.Now())

	alreadyPublished, err := s.db.IsPublished(v.ID())
	if err != nil {
		return err
	}

	if alreadyPublished {
		log.Println(v.ID() + " already published")
		return nil
	}

	err = v.Sync(s.daemon, s.claimAddress, publishAmount, s.LbryChannelName)
	if err != nil {
		return err
	}

	err = s.db.SetPublished(v.ID())
	if err != nil {
		return err
	}

	return nil
}

func startDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "start", "lbrynet.service").Run()
	if err != nil {
		return errors.New(err)
	}
	return nil
}

func stopDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "stop", "lbrynet.service").Run()
	if err != nil {
		return errors.New(err)
	}
	return nil
}

func getChannelIDFromFile(channelName string) (string, error) {
	channelsJSON, err := ioutil.ReadFile("./channels")
	if err != nil {
		return "", errors.Wrap(err, 0)
	}

	var channels map[string]string
	err = json.Unmarshal(channelsJSON, &channels)
	if err != nil {
		return "", errors.Wrap(err, 0)
	}

	channelID, ok := channels[channelName]
	if !ok {
		return "", errors.New("channel not in list")
	}

	return channelID, nil
}
