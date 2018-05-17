package ytsync

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/jsonrpc"
	"github.com/lbryio/lbry.go/stop"
	"github.com/lbryio/lbry.go/ytsync/redisdb"
	"github.com/lbryio/lbry.go/ytsync/sources"

	"github.com/lbryio/lbry.go/util"
	"github.com/mitchellh/go-ps"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	youtube "google.golang.org/api/youtube/v3"
)

const (
	channelClaimAmount = 0.01
	publishAmount      = 0.01
)

type video interface {
	ID() string
	IDAndNum() string
	PlaylistPosition() int
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
	Refill                  int

	daemon         *jsonrpc.Client
	claimAddress   string
	videoDirectory string
	db             *redisdb.DB

	grp *stop.Group

	wg    sync.WaitGroup
	queue chan video
}

func (s *Sync) FullCycle() error {
	var err error
	if os.Getenv("HOME") == "" {
		return errors.Err("no $HOME env var found")
	}

	if s.YoutubeChannelID == "" {
		channelID, err := getChannelIDFromFile(s.LbryChannelName)
		if err != nil {
			return err
		}
		s.YoutubeChannelID = channelID
	}
	defaultWalletDir := os.Getenv("HOME") + "/.lbryum/wallets/default_wallet"
	if os.Getenv("REGTEST") == "true" {
		defaultWalletDir = os.Getenv("HOME") + "/.lbryum_regtest/wallets/default_wallet"
	}
	walletBackupDir := os.Getenv("HOME") + "/wallets/" + strings.Replace(s.LbryChannelName, "@", "", 1)

	if _, err := os.Stat(defaultWalletDir); !os.IsNotExist(err) {
		return errors.Err("default_wallet already exists")
	}

	if _, err = os.Stat(walletBackupDir); !os.IsNotExist(err) {
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
			logShutdownError(shutdownErr)
		} else {
			// the cli will return long before the daemon effectively stops. we must observe the processes running
			// before moving the wallet
			var waitTimeout time.Duration = 60 * 8
			processDeathError := waitForDaemonProcess(waitTimeout)
			if processDeathError != nil {
				logShutdownError(processDeathError)
			} else {
				walletErr := os.Rename(defaultWalletDir, walletBackupDir)
				if walletErr != nil {
					log.Errorf("error moving wallet to backup dir: %v", walletErr)
				}
			}
		}
	}()

	s.videoDirectory, err = ioutil.TempDir("", "ytsync")
	if err != nil {
		return errors.Wrap(err, 0)
	}

	s.db = redisdb.New()
	s.grp = stop.New()
	s.queue = make(chan video)

	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interruptChan
		log.Println("Got interrupt signal, shutting down (if publishing, will shut down after current publish)")
		s.grp.Stop()
	}()

	log.Printf("Starting daemon")
	err = startDaemonViaSystemd()
	if err != nil {
		return err
	}

	log.Infoln("Waiting for daemon to finish starting...")
	s.daemon = jsonrpc.NewClient("")
	s.daemon.SetRPCTimeout(5 * time.Minute)

WaitForDaemonStart:
	for {
		select {
		case <-s.grp.Ch():
			return nil
		default:
			_, err := s.daemon.WalletBalance()
			if err == nil {
				break WaitForDaemonStart
			}
			time.Sleep(5 * time.Second)
		}
	}

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
func logShutdownError(shutdownErr error) {
	util.SendToSlackError("error shutting down daemon: %v", shutdownErr)
	util.SendToSlackError("WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR")
}

func (s *Sync) doSync() error {
	var err error

	err = s.walletSetup()
	if err != nil {
		return errors.Err("Initial wallet setup failed! Manual Intervention is required. Reason: %v", err)
	}

	if s.StopOnError {
		log.Println("Will stop publishing if an error is detected")
	}

	for i := 0; i < s.ConcurrentVideos; i++ {
		go s.startWorker(i)
	}

	if s.LbryChannelName == "@UCBerkeley" {
		err = s.enqueueUCBVideos()
	} else {
		err = s.enqueueYoutubeVideos()
	}
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
		case <-s.grp.Ch():
			log.Printf("Stopping worker %d", workerNum)
			return
		default:
		}

		select {
		case v, more = <-s.queue:
			if !more {
				return
			}
		case <-s.grp.Ch():
			log.Printf("Stopping worker %d", workerNum)
			return
		}

		log.Println("================================================================================")

		tryCount := 0
		for {
			tryCount++
			err := s.processVideo(v)

			if err != nil {
				logMsg := fmt.Sprintf("error processing video: " + err.Error())
				log.Errorln(logMsg)
				fatalErrors := []string{
					":5279: read: connection reset by peer",
					"net/http: request canceled (Client.Timeout exceeded while awaiting headers)",
				}
				if util.InSliceContains(err.Error(), fatalErrors) || s.StopOnError {
					s.grp.Stop()
				} else if s.MaxTries > 1 {
					errorsNoRetry := []string{
						"non 200 status code received",
						" reason: 'This video contains content from",
						"dont know which claim to update",
						"uploader has not made this video available in your country",
						"download error: AccessDenied: Access Denied",
						"Playback on other websites has been disabled by the video owner",
						"Error in daemon: Cannot publish empty file",
						"Error extracting sts from embedded url response",
					}
					if util.InSliceContains(err.Error(), errorsNoRetry) {
						log.Println("This error should not be retried at all")
					} else if tryCount < s.MaxTries {
						if strings.Contains(err.Error(), "The transaction was rejected by network rules.(258: txn-mempool-conflict)") ||
							strings.Contains(err.Error(), "failed: Not enough funds") ||
							strings.Contains(err.Error(), "Error in daemon: Insufficient funds, please deposit additional LBC") ||
							strings.Contains(err.Error(), "The transaction was rejected by network rules.(64: too-long-mempool-chain)") {
							log.Println("waiting for a block and refilling addresses before retrying")
							err = s.walletSetup()
							if err != nil {
								s.stop.Stop()
								util.SendToSlackError("Failed to setup the wallet for a refill: %v", err)
								break
							}
						}
						log.Println("Retrying")
						continue
					}
					util.SendToSlackError("Video failed after %d retries, skipping. Stack: %s", tryCount, logMsg)
				}
			}
			break
		}
	}
}

func (s *Sync) enqueueYoutubeVideos() error {
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Channels.List("contentDetails").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return errors.Prefix("error getting channels", err)
	}

	if len(response.Items) < 1 {
		return errors.Err("youtube channel not found")
	}

	if response.Items[0].ContentDetails.RelatedPlaylists == nil {
		return errors.Err("no related playlists")
	}

	playlistID := response.Items[0].ContentDetails.RelatedPlaylists.Uploads
	if playlistID == "" {
		return errors.Err("no channel playlist")
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
			return errors.Prefix("error getting playlist items", err)
		}

		if len(playlistResponse.Items) < 1 {
			return errors.Err("playlist items not found")
		}

		for _, item := range playlistResponse.Items {
			// todo: there's thumbnail info here. why did we need lambda???
			// because when videos are taken down from youtube, the thumb is gone too

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
		case <-s.grp.Ch():
			break Enqueue
		default:
		}

		select {
		case s.queue <- v:
		case <-s.grp.Ch():
			break Enqueue
		}
	}

	return nil
}

func (s *Sync) enqueueUCBVideos() error {
	var videos []video

	csvFile, err := os.Open("ucb.csv")
	if err != nil {
		return err
	}

	reader := csv.NewReader(bufio.NewReader(csvFile))
	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		data := struct {
			PublishedAt string `json:"publishedAt"`
		}{}
		err = json.Unmarshal([]byte(line[4]), &data)
		if err != nil {
			return err
		}

		videos = append(videos, sources.NewUCBVideo(line[0], line[2], line[1], line[3], data.PublishedAt, s.videoDirectory))
	}

	log.Printf("Publishing %d videos\n", len(videos))

	sort.Sort(byPublishedAt(videos))

Enqueue:
	for _, v := range videos {
		select {
		case <-s.grp.Ch():
			break Enqueue
		default:
		}

		select {
		case s.queue <- v:
		case <-s.grp.Ch():
			break Enqueue
		}
	}

	return nil
}

func (s *Sync) processVideo(v video) (err error) {
	defer func() {
		if p := recover(); p != nil {
			var ok bool
			err, ok = p.(error)
			if !ok {
				err = errors.Err("%v", p)
			}
			err = errors.Wrap(p, 2)
		}
	}()

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

	if v.PlaylistPosition() > 1000 {
		log.Println(v.ID() + " is old: skipping")
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
		return errors.Err(err)
	}
	return nil
}

func stopDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "stop", "lbrynet.service").Run()
	if err != nil {
		return errors.Err(err)
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
		return "", errors.Err("channel not in list")
	}

	return channelID, nil
}

// waitForDaemonProcess observes the running processes and returns when the process is no longer running or when the timeout is up
func waitForDaemonProcess(timeout time.Duration) error {
	processes, err := ps.Processes()
	if err != nil {
		return err
	}
	var daemonProcessId = -1
	for _, p := range processes {
		if p.Executable() == "lbrynet-daemon" {
			daemonProcessId = p.Pid()
			break
		}
	}
	if daemonProcessId == -1 {
		return nil
	}
	then := time.Now()
	stopTime := then.Add(time.Duration(timeout * time.Second))
	for !time.Now().After(stopTime) {
		wait := 10 * time.Second
		log.Println("the daemon is still running, waiting for it to exit")
		time.Sleep(wait)
		proc, err := os.FindProcess(daemonProcessId)
		if err != nil {
			// couldn't find the process, that means the daemon is stopped and can continue
			return nil
		}
		//double check if process is running and alive
		//by sending a signal 0
		//NOTE : syscall.Signal is not available in Windows
		err = proc.Signal(syscall.Signal(0))
		//the process doesn't exist anymore! we're free to go
		if err != nil && (err == syscall.ESRCH || err.Error() == "os: process already finished") {
			return nil
		}
	}
	return errors.Err("timeout reached")
}
