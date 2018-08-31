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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/jsonrpc"
	"github.com/lbryio/lbry.go/stop"
	"github.com/lbryio/lbry.go/util"
	"github.com/lbryio/lbry.go/ytsync/redisdb"
	"github.com/lbryio/lbry.go/ytsync/sources"
	"github.com/mitchellh/go-ps"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

const (
	channelClaimAmount = 0.01
	publishAmount      = 0.01
	maxReasonLength    = 500
)

type video interface {
	Size() *int64
	ID() string
	IDAndNum() string
	PlaylistPosition() int
	PublishedAt() time.Time
	Sync(*jsonrpc.Client, string, float64, string, int, map[string]bool, *sync.RWMutex) (*sources.SyncSummary, error)
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
	Manager                 *SyncManager
	LbrycrdString           string
	AwsS3ID                 string
	AwsS3Secret             string
	AwsS3Region             string
	AwsS3Bucket             string

	daemon          *jsonrpc.Client
	claimAddress    string
	videoDirectory  string
	db              *redisdb.DB
	syncedVideosMux *sync.RWMutex
	syncedVideos    map[string]syncedVideo
	claimNames      map[string]bool
	grp             *stop.Group
	lbryChannelID   string

	walletMux *sync.Mutex
	queue     chan video
}

func (s *Sync) AppendSyncedVideo(videoID string, published bool, failureReason string, claimName string) {
	s.syncedVideosMux.Lock()
	defer s.syncedVideosMux.Unlock()
	s.syncedVideos[videoID] = syncedVideo{
		VideoID:       videoID,
		Published:     published,
		FailureReason: failureReason,
	}
	if claimName != "" {
		s.claimNames[claimName] = true
	}
}

// SendErrorToSlack Sends an error message to the default channel and to the process log.
func SendErrorToSlack(format string, a ...interface{}) error {
	message := format
	if len(a) > 0 {
		message = fmt.Sprintf(format, a...)
	}
	log.Errorln(message)
	return util.SendToSlack(":sos: " + message)
}

// SendInfoToSlack Sends an info message to the default channel and to the process log.
func SendInfoToSlack(format string, a ...interface{}) error {
	message := format
	if len(a) > 0 {
		message = fmt.Sprintf(format, a...)
	}
	log.Infoln(message)
	return util.SendToSlack(":information_source: " + message)
}

// IsInterrupted can be queried to discover if the sync process was interrupted manually
func (s *Sync) IsInterrupted() bool {
	select {
	case <-s.grp.Ch():
		return true
	default:
		return false
	}
}

func (s *Sync) downloadWallet() error {
	defaultWalletDir := os.Getenv("HOME") + "/.lbryum/wallets/default_wallet"
	defaultTempWalletDir := os.Getenv("HOME") + "/.lbryum/wallets/tmp_wallet"
	key := aws.String("/wallets/" + s.YoutubeChannelID)
	if os.Getenv("REGTEST") == "true" {
		defaultWalletDir = os.Getenv("HOME") + "/.lbryum_regtest/wallets/default_wallet"
		defaultTempWalletDir = os.Getenv("HOME") + "/.lbryum_regtest/wallets/tmp_wallet"
		key = aws.String("/regtest/" + s.YoutubeChannelID)
	}

	if _, err := os.Stat(defaultWalletDir); !os.IsNotExist(err) {
		return errors.Err("default_wallet already exists")
	}

	creds := credentials.NewStaticCredentials(s.AwsS3ID, s.AwsS3Secret, "")
	s3Session, err := session.NewSession(&aws.Config{Region: aws.String(s.AwsS3Region), Credentials: creds})
	if err != nil {
		return err
	}
	downloader := s3manager.NewDownloader(s3Session)
	out, err := os.Create(defaultTempWalletDir)
	if err != nil {
		return err
	}
	defer out.Close()

	bytesWritten, err := downloader.Download(out, &s3.GetObjectInput{
		Bucket: aws.String(s.AwsS3Bucket),
		Key:    key,
	})
	if err != nil {
		// Casting to the awserr.Error type will allow you to inspect the error
		// code returned by the service in code. The error code can be used
		// to switch on context specific functionality. In this case a context
		// specific error message is printed to the user based on the bucket
		// and key existing.
		//
		// For information on other S3 API error codes see:
		// http://docs.aws.amazon.com/AmazonS3/latest/API/ErrorResponses.html
		if aerr, ok := err.(awserr.Error); ok {
			code := aerr.Code()
			if code == s3.ErrCodeNoSuchKey {
				return errors.Err("wallet not on S3")
			}
		}
		return err
	} else if bytesWritten == 0 {
		return errors.Err("zero bytes written")
	}

	return os.Rename(defaultTempWalletDir, defaultWalletDir)
}

func (s *Sync) uploadWallet() error {
	defaultWalletDir := os.Getenv("HOME") + "/.lbryum/wallets/default_wallet"
	key := aws.String("/wallets/" + s.YoutubeChannelID)
	if os.Getenv("REGTEST") == "true" {
		defaultWalletDir = os.Getenv("HOME") + "/.lbryum_regtest/wallets/default_wallet"
		key = aws.String("/regtest/" + s.YoutubeChannelID)
	}

	if _, err := os.Stat(defaultWalletDir); os.IsNotExist(err) {
		return errors.Err("default_wallet does not exist")
	}

	creds := credentials.NewStaticCredentials(s.AwsS3ID, s.AwsS3Secret, "")
	s3Session, err := session.NewSession(&aws.Config{Region: aws.String(s.AwsS3Region), Credentials: creds})
	if err != nil {
		return err
	}

	uploader := s3manager.NewUploader(s3Session)

	file, err := os.Open(defaultWalletDir)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s.AwsS3Bucket),
		Key:    key,
		Body:   file,
	})
	if err != nil {
		return err
	}

	return os.Remove(defaultWalletDir)
}

func (s *Sync) FullCycle() (e error) {
	if os.Getenv("HOME") == "" {
		return errors.Err("no $HOME env var found")
	}
	if s.YoutubeChannelID == "" {
		return errors.Err("channel ID not provided")
	}
	s.syncedVideosMux = &sync.RWMutex{}
	s.walletMux = &sync.Mutex{}
	s.db = redisdb.New()
	s.grp = stop.New()
	s.queue = make(chan video)
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interruptChan)
	go func() {
		<-interruptChan
		log.Println("Got interrupt signal, shutting down (if publishing, will shut down after current publish)")
		s.grp.Stop()
	}()
	syncedVideos, claimNames, err := s.Manager.setChannelStatus(s.YoutubeChannelID, StatusSyncing, "")
	if err != nil {
		return err
	}
	s.syncedVideosMux.Lock()
	s.syncedVideos = syncedVideos
	s.claimNames = claimNames
	s.syncedVideosMux.Unlock()

	defer s.updateChannelStatus(&e)

	err = s.downloadWallet()
	if err != nil && err.Error() != "wallet not on S3" {
		return errors.Prefix("failure in downloading wallet: ", err)
	} else if err == nil {
		log.Println("Continuing previous upload")
	} else {
		log.Println("Starting new wallet")
	}

	defer s.stopAndUploadWallet(&e)

	s.videoDirectory, err = ioutil.TempDir("", "ytsync")
	if err != nil {
		return errors.Wrap(err, 0)
	}

	log.Printf("Starting daemon")
	err = startDaemonViaSystemd()
	if err != nil {
		return err
	}

	log.Infoln("Waiting for daemon to finish starting...")
	s.daemon = jsonrpc.NewClient("")
	s.daemon.SetRPCTimeout(40 * time.Minute)

	err = s.waitForDaemonStart()
	if err != nil {
		return err
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
func (s *Sync) updateChannelStatus(e *error) {
	if *e != nil {
		//conditions for which a channel shouldn't be marked as failed
		noFailConditions := []string{
			"this youtube channel is being managed by another server",
		}
		if util.SubstringInSlice((*e).Error(), noFailConditions) {
			return
		}
		failureReason := (*e).Error()
		_, _, err := s.Manager.setChannelStatus(s.YoutubeChannelID, StatusFailed, failureReason)
		if err != nil {
			msg := fmt.Sprintf("Failed setting failed state for channel %s.", s.LbryChannelName)
			err = errors.Prefix(msg, err)
			*e = errors.Prefix(err.Error(), *e)
		}
	} else if !s.IsInterrupted() {
		_, _, err := s.Manager.setChannelStatus(s.YoutubeChannelID, StatusSynced, "")
		if err != nil {
			*e = err
		}
	}
}

func (s *Sync) waitForDaemonStart() error {

	for {
		select {
		case <-s.grp.Ch():
			return errors.Err("interrupted during daemon startup")
		default:
			s, err := s.daemon.Status()
			if err == nil && s.StartupStatus.Wallet {
				return nil
			}
			time.Sleep(5 * time.Second)
		}
	}
}
func (s *Sync) stopAndUploadWallet(e *error) {
	log.Printf("Stopping daemon")
	shutdownErr := stopDaemonViaSystemd()
	if shutdownErr != nil {
		logShutdownError(shutdownErr)
	} else {
		// the cli will return long before the daemon effectively stops. we must observe the processes running
		// before moving the wallet
		waitTimeout := 8 * time.Minute
		processDeathError := waitForDaemonProcess(waitTimeout)
		if processDeathError != nil {
			logShutdownError(processDeathError)
		} else {
			err := s.uploadWallet()
			if err != nil {
				if *e == nil {
					e = &err //not 100% sure
					return
				} else {
					*e = errors.Prefix("failure uploading wallet: ", *e)
				}
			}
		}
	}
}
func logShutdownError(shutdownErr error) {
	SendErrorToSlack("error shutting down daemon: %v", shutdownErr)
	SendErrorToSlack("WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR")
}

func hasDupes(claims []jsonrpc.Claim) (bool, error) {
	videoIDs := make(map[string]interface{})
	for _, c := range claims {
		if !util.InSlice(c.Category, []string{"claim", "update"}) || c.Value.Stream == nil {
			continue
		}
		if c.Value.Stream.Metadata == nil || c.Value.Stream.Metadata.Thumbnail == nil {
			return false, errors.Err("something is wrong with the this claim: %s", c.ClaimID)
		}
		tn := *c.Value.Stream.Metadata.Thumbnail
		videoID := tn[:strings.LastIndex(tn, "/")+1]
		_, ok := videoIDs[videoID]
		if !ok {
			videoIDs[videoID] = nil
			continue
		}
		return true, nil
	}
	return false, nil
}

//publishesCount counts the amount of videos published so far
func publishesCount(claims []jsonrpc.Claim) (int, error) {
	count := 0
	for _, c := range claims {
		if !util.InSlice(c.Category, []string{"claim", "update"}) || c.Value.Stream == nil {
			continue
		}
		if c.Value.Stream.Metadata == nil || c.Value.Stream.Metadata.Thumbnail == nil {
			return count, errors.Err("something is wrong with the this claim: %s", c.ClaimID)
		}
		count++
	}
	return count, nil
}

func (s *Sync) doSync() error {
	var err error
	claims, err := s.daemon.ClaimListMine()
	if err != nil {
		return errors.Prefix("cannot list claims: ", err)
	}
	hasDupes, err := hasDupes(*claims)
	if err != nil {
		return errors.Prefix("error checking for duplicates: ", err)
	}
	if hasDupes {
		return errors.Err("channel has duplicates! Manual fix required")
	}
	pubsOnWallet, err := publishesCount(*claims)
	if err != nil {
		return errors.Prefix("error counting claims: ", err)
	}
	pubsOnDB := 0
	for _, sv := range s.syncedVideos {
		if sv.Published {
			pubsOnDB++
		}
	}
	if pubsOnWallet > pubsOnDB {
		return errors.Err("not all published videos are in the database")
	}
	if pubsOnWallet < pubsOnDB {
		SendInfoToSlack("We're claiming to have published %d videos but we only published %d (%s)", pubsOnDB, pubsOnWallet, s.YoutubeChannelID)
	}
	err = s.walletSetup()
	if err != nil {
		return errors.Prefix("Initial wallet setup failed! Manual Intervention is required.", err)
	}

	if s.StopOnError {
		log.Println("Will stop publishing if an error is detected")
	}

	for i := 0; i < s.ConcurrentVideos; i++ {
		s.grp.Add(1)
		go func(i int) {
			defer s.grp.Done()
			s.startWorker(i)
		}(i)
	}

	if s.LbryChannelName == "@UCBerkeley" {
		err = s.enqueueUCBVideos()
	} else {
		err = s.enqueueYoutubeVideos()
	}
	close(s.queue)
	s.grp.Wait()
	return err
}

func (s *Sync) startWorker(workerNum int) {
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
					"no space left on device",
					"NotEnoughFunds",
					"Cannot publish using channel",
					"cannot concatenate 'str' and 'NoneType' objects",
					"more than 90% of the space has been used.",
				}
				if util.SubstringInSlice(err.Error(), fatalErrors) || s.StopOnError {
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
						"Client.Timeout exceeded while awaiting headers)",
						"the video is too big to sync, skipping for now",
					}
					if util.SubstringInSlice(err.Error(), errorsNoRetry) {
						log.Println("This error should not be retried at all")
					} else if tryCount < s.MaxTries {
						if strings.Contains(err.Error(), "txn-mempool-conflict") ||
							strings.Contains(err.Error(), "too-long-mempool-chain") {
							log.Println("waiting for a block before retrying")
							err = s.waitForNewBlock()
							if err != nil {
								s.grp.Stop()
								SendErrorToSlack("something went wrong while waiting for a block: %v", err)
								break
							}
						} else if strings.Contains(err.Error(), "failed: Not enough funds") ||
							strings.Contains(err.Error(), "Error in daemon: Insufficient funds, please deposit additional LBC") {
							log.Println("refilling addresses before retrying")
							err = s.walletSetup()
							if err != nil {
								s.grp.Stop()
								SendErrorToSlack("failed to setup the wallet for a refill: %v", err)
								break
							}
						}
						log.Println("Retrying")
						continue
					}
					SendErrorToSlack("Video failed after %d retries, skipping. Stack: %s", tryCount, logMsg)
				}
				s.AppendSyncedVideo(v.ID(), false, err.Error(), "")
				err = s.Manager.MarkVideoStatus(s.YoutubeChannelID, v.ID(), VideoStatusFailed, "", "", err.Error(), v.Size())
				if err != nil {
					SendErrorToSlack("Failed to mark video on the database: %s", err.Error())
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

	s.syncedVideosMux.RLock()
	sv, ok := s.syncedVideos[v.ID()]
	s.syncedVideosMux.RUnlock()
	alreadyPublished := ok && sv.Published

	neverRetryFailures := []string{
		"Error extracting sts from embedded url response",
		"the video is too big to sync, skipping for now",
	}
	if ok && !sv.Published && util.SubstringInSlice(sv.FailureReason, neverRetryFailures) {
		log.Println(v.ID() + " can't ever be published")
		return nil
	}

	//TODO: remove this after a few runs...
	alreadyPublishedOld, err := s.db.IsPublished(v.ID())
	if err != nil {
		return err
	}
	//TODO: remove this after a few runs...
	if alreadyPublishedOld && !alreadyPublished {
		//seems like something in the migration of blobs didn't go perfectly right so warn about it!
		SendInfoToSlack("A video that was previously published is on the local database but isn't on the remote db! fix it @Nikooo777! \nchannelID: %s, videoID: %s",
			s.YoutubeChannelID, v.ID())
		return nil
	}

	if alreadyPublished {
		log.Println(v.ID() + " already published")
		return nil
	}

	if v.PlaylistPosition() > s.Manager.VideosLimit {
		log.Println(v.ID() + " is old: skipping")
		return nil
	}
	err = s.Manager.checkUsedSpace()
	if err != nil {
		return err
	}
	summary, err := v.Sync(s.daemon, s.claimAddress, publishAmount, s.lbryChannelID, s.Manager.MaxVideoSize, s.claimNames, s.syncedVideosMux)
	if err != nil {
		return err
	}
	err = s.Manager.MarkVideoStatus(s.YoutubeChannelID, v.ID(), VideoStatusPublished, summary.ClaimID, summary.ClaimName, "", v.Size())
	if err != nil {
		SendErrorToSlack("Failed to mark video on the database: %s", err.Error())
	}

	s.AppendSyncedVideo(v.ID(), true, "", summary.ClaimName)

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
