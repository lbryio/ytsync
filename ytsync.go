package ytsync

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbryio/lbry.go/jsonrpc"

	"github.com/garyburd/redigo/redis"
	"github.com/go-errors/errors"
	ytdl "github.com/kkdai/youtube"
	"github.com/lbryio/lbry.go/lbrycrd"
	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

const (
	redisHashKey       = "ytsync"
	redisSyncedVal     = "t"
	channelClaimAmount = 0.01
	publishAmount      = 0.01
)

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
	redisPool      *redis.Pool
}

func (s *Sync) initDaemon() {
	if s.daemon == nil {
		s.daemon = jsonrpc.NewClient("")
		log.Infoln("Waiting for daemon to finish starting...")
		for {
			_, err := s.daemon.WalletBalance()
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}

func (s *Sync) init() error {
	var err error

	s.redisPool = &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 5 * time.Minute,
		Dial:        func() (redis.Conn, error) { return redis.Dial("tcp", ":6379") },
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}

	s.videoDirectory, err = ioutil.TempDir("", "ytsync")
	if err != nil {
		return errors.Wrap(err, 0)
	}

	s.initDaemon()

	address, err := s.daemon.WalletUnusedAddress()
	if err != nil {
		return err
	} else if address == nil {
		return errors.New("could not get unused address")
	}
	s.claimAddress = string(*address)
	if s.claimAddress == "" {
		return errors.New("found blank claim address")
	}

	err = s.ensureEnoughUTXOs()
	if err != nil {
		return err
	}

	if s.LbryChannelName != "" {
		err = s.ensureChannelOwnership()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Sync) CountVideos() (uint64, error) {
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return 0, errors.WrapPrefix(err, "error creating YouTube service", 0)
	}

	response, err := service.Channels.List("statistics").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return 0, errors.WrapPrefix(err, "error getting channels", 0)
	}

	if len(response.Items) < 1 {
		return 0, errors.New("youtube channel not found")
	}

	return response.Items[0].Statistics.VideoCount, nil
}

func (s *Sync) FullCycle() error {
	if os.Getenv("HOME") == "" {
		return errors.New("no $HOME env var found")
	}

	newChannel := true

	lbryumDir := os.Getenv("HOME") + "/.lbryum"
	walletDir := os.Getenv("HOME") + "/wallets/" + strings.Replace(s.LbryChannelName, "@", "", 1)
	if _, err := os.Stat(walletDir); !os.IsNotExist(err) {
		newChannel = false
		err = os.Rename(walletDir, lbryumDir)
		if err != nil {
			return errors.Wrap(err, 0)
		}
		log.Println("Continuing previous upload")
	}

	err := s.startDaemonViaSystemd()
	if err != nil {
		return err
	}

	if newChannel {
		s.initDaemon()

		addressResp, err := s.daemon.WalletUnusedAddress()
		if err != nil {
			return err
		} else if addressResp == nil {
			return errors.New("no response")
		}
		address := string(*addressResp)

		count, err := s.CountVideos()
		if err != nil {
			return err
		}
		initialAmount := float64(count)*publishAmount + channelClaimAmount
		initialAmount += initialAmount * 0.1 // add 10% margin for fees, etc

		log.Printf("Loading wallet with %f initial credits", initialAmount)
		lbrycrdd, err := lbrycrd.NewWithDefaultURL()
		if err != nil {
			return err
		}
		lbrycrdd.SimpleSend(address, initialAmount)
		//lbrycrdd.SendWithSplit(address, initialAmount, 50)

		wait := 15 * time.Second
		log.Println("Waiting " + wait.String() + " for lbryum to let us know we have the new transaction")
		time.Sleep(wait)

		log.Println("Waiting for transaction to be confirmed")
		err = s.waitUntilUTXOsConfirmed()
		if err != nil {
			return err
		}
	}

	err = s.Go()
	if err != nil {
		return err
	}

	// wait for reflection to finish???
	wait := 15 * time.Second // should bump this up to a few min, but keeping it low for testing
	log.Println("Waiting " + wait.String() + " to finish reflecting everything")
	time.Sleep(wait)

	log.Printf("Stopping daemon")
	err = s.stopDaemonViaSystemd()
	if err != nil {
		return err
	}

	err = os.Rename(lbryumDir, walletDir)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	return nil
}

func (s *Sync) Go() error {
	var err error

	err = s.init()
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	videoQueue := make(chan video)

	queueStopChan := make(chan struct{})
	sendStopEnqueuing := sync.Once{}

	var videoErrored atomic.Value
	videoErrored.Store(false)
	if s.StopOnError {
		log.Println("Will stop publishing if an error is detected")
	}

	for i := 0; i < s.ConcurrentVideos; i++ {
		go func() {
			wg.Add(1)
			defer wg.Done()

			for {
				v, more := <-videoQueue
				if !more {
					return
				}
				if s.StopOnError && videoErrored.Load().(bool) {
					log.Println("Video errored. Exiting")
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
							videoErrored.Store(true)
							sendStopEnqueuing.Do(func() {
								queueStopChan <- struct{}{}
							})
						} else if s.MaxTries > 1 {
							if strings.Contains(err.Error(), "non 200 status code received") ||
								strings.Contains(err.Error(), " reason: 'This video contains content from") ||
								strings.Contains(err.Error(), "Playback on other websites has been disabled by the video owner") {
								log.Println("This error should not be retried at all")
							} else if tryCount >= s.MaxTries {
								log.Println("Video failed after " + strconv.Itoa(s.MaxTries) + " retries, exiting")
								videoErrored.Store(true)
								sendStopEnqueuing.Do(func() {
									queueStopChan <- struct{}{}
								})
							} else {
								log.Println("Retrying")
								continue
							}
						}
					}
					break
				}
			}
		}()
	}

	err = s.enqueueVideosFromChannel(s.YoutubeChannelID, &videoQueue, &queueStopChan)
	close(videoQueue)
	wg.Wait()
	return err
}

func allUTXOsConfirmed(utxolist *jsonrpc.UTXOListResponse) bool {
	if utxolist == nil {
		return false
	}

	if len(*utxolist) < 1 {
		return false
	} else {
		for _, utxo := range *utxolist {
			if utxo.Height == 0 {
				return false
			}
		}
	}

	return true
}

func (s *Sync) ensureEnoughUTXOs() error {
	utxolist, err := s.daemon.UTXOList()
	if err != nil {
		return err
	} else if utxolist == nil {
		return errors.New("no response")
	}

	if !allUTXOsConfirmed(utxolist) {
		log.Println("Waiting for previous txns to confirm") // happens if you restarted the daemon soon after a previous publish run
		s.waitUntilUTXOsConfirmed()
	}

	target := 50
	count := 0

	for _, utxo := range *utxolist {
		if !utxo.IsClaim && !utxo.IsSupport && !utxo.IsUpdate && utxo.Amount.Cmp(decimal.New(0, 0)) == 1 {
			count++
		}
	}

	if count < target {
		newAddresses := target - count

		balance, err := s.daemon.WalletBalance()
		if err != nil {
			return err
		} else if balance == nil {
			return errors.New("no response")
		}

		log.Println("balance is " + decimal.Decimal(*balance).String())

		amountPerAddress := decimal.Decimal(*balance).Div(decimal.NewFromFloat(float64(target)))
		log.Infof("Putting %s credits into each of %d new addresses", amountPerAddress.String(), newAddresses)
		prefillTx, err := s.daemon.WalletPrefillAddresses(newAddresses, amountPerAddress, true)
		if err != nil {
			return err
		} else if prefillTx == nil {
			return errors.New("no response")
		} else if !prefillTx.Complete || !prefillTx.Broadcast {
			return errors.New("failed to prefill addresses")
		}

		wait := 15 * time.Second
		log.Println("Waiting " + wait.String() + " for lbryum to let us know we have the new addresses")
		time.Sleep(wait)

		log.Println("Creating UTXOs and waiting for them to be confirmed")
		err = s.waitUntilUTXOsConfirmed()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Sync) waitUntilUTXOsConfirmed() error {
	for {
		r, err := s.daemon.UTXOList()
		if err != nil {
			return err
		} else if r == nil {
			return errors.New("no response")
		}

		if allUTXOsConfirmed(r) {
			return nil
		}

		wait := 30 * time.Second
		log.Println("Waiting " + wait.String() + "...")
		time.Sleep(wait)
	}
}

func (s *Sync) ensureChannelOwnership() error {
	channels, err := s.daemon.ChannelListMine()
	if err != nil {
		return err
	} else if channels == nil {
		return errors.New("no channels")
	}

	for _, channel := range *channels {
		if channel.Name == s.LbryChannelName {
			return nil
		}
	}

	resolveResp, err := s.daemon.Resolve(s.LbryChannelName)
	if err != nil {
		return err
	}

	channel := (*resolveResp)[s.LbryChannelName]
	channelBidAmount := channelClaimAmount

	channelNotFound := channel.Error != nil && strings.Contains(*(channel.Error), "cannot be resolved")
	if !channelNotFound {
		if !s.TakeOverExistingChannel {
			return errors.New("Channel exists and we don't own it. Pick another channel.")
		}
		log.Println("Channel exists and we don't own it. Outbidding existing claim.")
		channelBidAmount, _ = channel.Certificate.Amount.Add(decimal.NewFromFloat(channelClaimAmount)).Float64()
	}

	_, err = s.daemon.ChannelNew(s.LbryChannelName, channelBidAmount)
	if err != nil {
		return err
	}

	// niko's code says "unfortunately the queues in the daemon are not yet merged so we must give it some time for the channel to go through"
	wait := 15 * time.Second
	log.Println("Waiting " + wait.String() + " for channel claim to go through")
	time.Sleep(wait)

	return nil
}

func (s *Sync) enqueueVideosFromChannel(channelID string, videoChan *chan video, queueStopChan *chan struct{}) error {
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return errors.WrapPrefix(err, "error creating YouTube service", 0)
	}

	response, err := service.Channels.List("contentDetails").Id(channelID).Do()
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

	videos := []video{}

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
			publishedAt, err := time.Parse(time.RFC3339Nano, item.Snippet.PublishedAt)
			if err != nil {
				return errors.WrapPrefix(err, "failed to parse time", 0)
			}

			// normally we'd send the video into the channel here, but youtube api doesn't have sorting
			// so we have to get ALL the videos, then sort them, then send them in
			videos = append(videos, video{
				id:               item.Snippet.ResourceId.VideoId,
				channelID:        channelID,
				title:            item.Snippet.Title,
				description:      item.Snippet.Description,
				channelTitle:     item.Snippet.ChannelTitle,
				playlistPosition: item.Snippet.Position,
				publishedAt:      publishedAt,
				dir:              s.videoDirectory,
			})
		}

		log.Infoln("Got info for " + strconv.Itoa(len(videos)) + " videos from youtube API")

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
		case *videoChan <- v:
		case <-*queueStopChan:
			break Enqueue
		}
	}

	return nil
}

func (s *Sync) processVideo(v video) error {
	log.Println("Processing " + v.id + " (" + strconv.Itoa(int(v.playlistPosition)) + " in channel)")
	defer func(start time.Time) {
		log.Println(v.id + " took " + time.Since(start).String())
	}(time.Now())

	conn := s.redisPool.Get()
	defer conn.Close()

	alreadyPublished, err := redis.String(conn.Do("HGET", redisHashKey, v.id))
	if err != nil && err != redis.ErrNil {
		return errors.WrapPrefix(err, "redis error", 0)

	}
	if alreadyPublished == redisSyncedVal {
		log.Println(v.id + " already published")
		return nil
	}

	//download and thumbnail can be done in parallel
	err = downloadVideo(v)
	if err != nil {
		return errors.WrapPrefix(err, "download error", 0)
	}

	err = triggerThumbnailSave(v.id)
	if err != nil {
		return errors.WrapPrefix(err, "thumbnail error", 0)
	}

	err = s.publish(v, conn)
	if err != nil {
		return errors.WrapPrefix(err, "publish error", 0)
	}

	return nil
}

func downloadVideo(v video) error {
	verbose := false
	videoPath := v.getFilename()

	_, err := os.Stat(videoPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if err == nil {
		log.Println(v.id + " already exists at " + videoPath)
		return nil
	}

	downloader := ytdl.NewYoutube(verbose)
	err = downloader.DecodeURL("https://www.youtube.com/watch?v=" + v.id)
	if err != nil {
		return err
	}
	err = downloader.StartDownload(videoPath)
	if err != nil {
		return err
	}
	log.Debugln("Downloaded " + v.id)
	return nil
}

func triggerThumbnailSave(videoID string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	params, err := json.Marshal(map[string]string{"videoid": videoID})
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

	log.Debugln("Created thumbnail for " + videoID)

	return nil
}

func strPtr(s string) *string { return &s }

func (s *Sync) publish(v video, conn redis.Conn) error {
	options := jsonrpc.PublishOptions{
		Title:        &v.title,
		Author:       &v.channelTitle,
		Description:  strPtr(v.getAbbrevDescription() + "\nhttps://www.youtube.com/watch?v=" + v.id),
		Language:     strPtr("en"),
		ClaimAddress: &s.claimAddress,
		Thumbnail:    strPtr("http://berk.ninja/thumbnails/" + v.id),
		License:      strPtr("Copyrighted (contact author)"),
	}
	if s.LbryChannelName != "" {
		options.ChannelName = &s.LbryChannelName
	}

	_, err := s.daemon.Publish(v.getClaimName(), v.getFilename(), publishAmount, options)
	if err != nil {
		return err
	}

	_, err = redis.Bool(conn.Do("HSET", redisHashKey, v.id, redisSyncedVal))
	if err != nil {
		return errors.New("redis error: " + err.Error())
	}

	return nil
}

func (s *Sync) startDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "start", "lbrynet.service").Run()
	if err != nil {
		return errors.New(err)
	}
	return nil
}

func (s *Sync) stopDaemonViaSystemd() error {
	err := exec.Command("/usr/bin/sudo", "/bin/systemctl", "stop", "lbrynet.service").Run()
	if err != nil {
		return errors.New(err)
	}
	return nil
}
