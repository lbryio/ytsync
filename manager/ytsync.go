package manager

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lbryio/ytsync/namer"
	"github.com/lbryio/ytsync/sdk"
	"github.com/lbryio/ytsync/sources"
	"github.com/lbryio/ytsync/thumbs"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/stop"
	"github.com/lbryio/lbry.go/extras/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
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
	Sync(*jsonrpc.Client, sources.SyncParams, *sdk.SyncedVideo, bool) (*sources.SyncSummary, error)
}

// sorting videos
type byPublishedAt []video

func (a byPublishedAt) Len() int           { return len(a) }
func (a byPublishedAt) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byPublishedAt) Less(i, j int) bool { return a[i].PublishedAt().Before(a[j].PublishedAt()) }

// Sync stores the options that control how syncing happens
type Sync struct {
	APIConfig               *sdk.APIConfig
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
	Fee                     *sdk.Fee
	daemon                  *jsonrpc.Client
	claimAddress            string
	videoDirectory          string
	syncedVideosMux         *sync.RWMutex
	syncedVideos            map[string]sdk.SyncedVideo
	grp                     *stop.Group
	lbryChannelID           string
	namer                   *namer.Namer
	walletMux               *sync.Mutex
	queue                   chan video
}

func (s *Sync) AppendSyncedVideo(videoID string, published bool, failureReason string, claimName string) {
	s.syncedVideosMux.Lock()
	defer s.syncedVideosMux.Unlock()
	s.syncedVideos[videoID] = sdk.SyncedVideo{
		VideoID:       videoID,
		Published:     published,
		FailureReason: failureReason,
	}
}

// SendErrorToSlack Sends an error message to the default channel and to the process log.
func SendErrorToSlack(format string, a ...interface{}) {
	message := format
	if len(a) > 0 {
		message = fmt.Sprintf(format, a...)
	}
	log.Errorln(message)
	err := util.SendToSlack(":sos: " + message)
	if err != nil {
		log.Errorln(err)
	}
}

// SendInfoToSlack Sends an info message to the default channel and to the process log.
func SendInfoToSlack(format string, a ...interface{}) {
	message := format
	if len(a) > 0 {
		message = fmt.Sprintf(format, a...)
	}
	log.Infoln(message)
	err := util.SendToSlack(":information_source: " + message)
	if err != nil {
		log.Errorln(err)
	}
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

func (s *Sync) setStatusSyncing() error {
	syncedVideos, claimNames, err := s.Manager.apiConfig.SetChannelStatus(s.YoutubeChannelID, StatusSyncing, "")
	if err != nil {
		return err
	}
	s.syncedVideosMux.Lock()
	s.syncedVideos = syncedVideos
	s.namer.SetNames(claimNames)
	s.syncedVideosMux.Unlock()
	return nil
}

func (s *Sync) setExceptions() {
	if s.YoutubeChannelID == "UCwjQfNRW6sGYb__pd7d4nUg" { //@FreeTalkLive
		s.Manager.maxVideoLength = 0.0 // skips max length checks
		s.Manager.maxVideoSize = 0
	}
}

func (s *Sync) FullCycle() (e error) {
	if os.Getenv("HOME") == "" {
		return errors.Err("no $HOME env var found")
	}
	if s.YoutubeChannelID == "" {
		return errors.Err("channel ID not provided")
	}

	s.setExceptions()

	s.syncedVideosMux = &sync.RWMutex{}
	s.walletMux = &sync.Mutex{}
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
	err := s.setStatusSyncing()
	if err != nil {
		return err
	}

	defer s.setChannelTerminationStatus(&e)

	err = s.downloadWallet()
	if err != nil && err.Error() != "wallet not on S3" {
		return errors.Prefix("failure in downloading wallet", err)
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

func (s *Sync) setChannelTerminationStatus(e *error) {
	if *e != nil {
		//conditions for which a channel shouldn't be marked as failed
		noFailConditions := []string{
			"this youtube channel is being managed by another server",
			"interrupted during daemon startup",
			"playlist items not found",
		}
		if util.SubstringInSlice((*e).Error(), noFailConditions) {
			return
		}
		failureReason := (*e).Error()
		_, _, err := s.Manager.apiConfig.SetChannelStatus(s.YoutubeChannelID, StatusFailed, failureReason)
		if err != nil {
			msg := fmt.Sprintf("Failed setting failed state for channel %s", s.LbryChannelName)
			*e = errors.Prefix(msg+err.Error(), *e)
		}
	} else if !s.IsInterrupted() {
		_, _, err := s.Manager.apiConfig.SetChannelStatus(s.YoutubeChannelID, StatusSynced, "")
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
			if err == nil && s.StartupStatus.Wallet && s.IsRunning {
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
					e = &err
					return
				} else {
					*e = errors.Prefix("failure uploading wallet", *e)
				}
			}
		}
	}
}
func logShutdownError(shutdownErr error) {
	SendErrorToSlack("error shutting down daemon: %v", shutdownErr)
	SendErrorToSlack("WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR")
}

var thumbnailHosts = []string{
	"berk.ninja/thumbnails/",
	thumbs.ThumbnailEndpoint,
}

func isYtsyncClaim(c jsonrpc.Claim) bool {
	if !util.InSlice(c.Type, []string{"claim", "update"}) || c.Value.GetStream() == nil {
		return false
	}
	if c.Value.GetThumbnail() == nil || c.Value.GetThumbnail().GetUrl() == "" {
		//most likely a claim created outside of ytsync, ignore!
		return false
	}

	for _, th := range thumbnailHosts {
		if strings.Contains(c.Value.GetThumbnail().GetUrl(), th) {
			return true
		}
	}
	return false
}

// fixDupes abandons duplicate claims
func (s *Sync) fixDupes(claims []jsonrpc.Claim) (bool, error) {
	abandonedClaims := false
	videoIDs := make(map[string]jsonrpc.Claim)
	for _, c := range claims {
		if !isYtsyncClaim(c) {
			continue
		}
		tn := c.Value.GetThumbnail().GetUrl()
		videoID := tn[strings.LastIndex(tn, "/")+1:]

		log.Infof("claimid: %s, claimName: %s, videoID: %s", c.ClaimID, c.Name, videoID)
		cl, ok := videoIDs[videoID]
		if !ok || cl.ClaimID == c.ClaimID {
			videoIDs[videoID] = c
			continue
		}
		// only keep the most recent one
		claimToAbandon := c
		videoIDs[videoID] = cl
		if c.Height > cl.Height {
			claimToAbandon = cl
			videoIDs[videoID] = c
		}
		log.Debugf("abandoning %+v", claimToAbandon)
		_, err := s.daemon.StreamAbandon(claimToAbandon.Txid, claimToAbandon.Nout, nil, false)
		if err != nil {
			return true, err
		}
		abandonedClaims = true
		//return true, nil
	}
	return abandonedClaims, nil
}

//updateRemoteDB counts the amount of videos published so far and updates the remote db if some videos weren't marked as published
//additionally it removes all entries in the database indicating that a video is published when it's actually not
func (s *Sync) updateRemoteDB(claims []jsonrpc.Claim) (total, fixed, removed int, err error) {
	count := 0
	videoIDMap := make(map[string]string, len(claims))
	for _, c := range claims {
		if !isYtsyncClaim(c) {
			continue
		}
		count++
		//check if claimID is in remote db
		tn := c.Value.GetThumbnail().GetUrl()
		videoID := tn[strings.LastIndex(tn, "/")+1:]
		videoIDMap[videoID] = c.ClaimID
		pv, ok := s.syncedVideos[videoID]
		if !ok || pv.ClaimName != c.Name {
			fixed++
			log.Debugf("adding %s to the database", c.Name)
			size, err := c.GetStreamSizeByMagic()
			if err != nil {
				size = 0
			}
			metadataVersion := uint(1)
			claimIsUpgraded := strings.Contains(tn, thumbs.ThumbnailEndpoint)
			if claimIsUpgraded {
				metadataVersion = 2
			}
			err = s.Manager.apiConfig.MarkVideoStatus(s.YoutubeChannelID, videoID, VideoStatusPublished, c.ClaimID, c.Name, "", util.PtrToInt64(int64(size)), metadataVersion)
			if err != nil {
				return count, fixed, 0, err
			}
		}
	}
	idsToRemove := make([]string, 0, len(videoIDMap))
	for vID, sv := range s.syncedVideos {
		_, ok := videoIDMap[vID]
		if !ok && sv.Published {
			log.Debugf("%s: claims to be published but wasn't found in the list of claims and will be removed if --remove-db-unpublished was specified", vID)
			idsToRemove = append(idsToRemove, vID)
		}
	}
	removeCount := 0
	if s.Manager.removeDBUnpublished && len(idsToRemove) > 0 {
		err := s.Manager.apiConfig.DeleteVideos(idsToRemove)
		if err != nil {
			return count, fixed, 0, err
		}
		removeCount++
	}
	return count, fixed, removeCount, nil
}

func (s *Sync) getClaims() ([]jsonrpc.Claim, error) {
	totalPages := uint64(1)
	var allClaims []jsonrpc.Claim
	for page := uint64(1); page <= totalPages; page++ {
		claims, err := s.daemon.ClaimList(nil, page, 50)
		if err != nil {
			return nil, errors.Prefix("cannot list claims", err)
		}
		allClaims = append(allClaims, (*claims).Claims...)
		totalPages = (*claims).TotalPages
	}
	return allClaims, nil
}

func (s *Sync) doSync() error {
	err := s.enableAddressReuse()
	if err != nil {
		return errors.Prefix("could not set address reuse policy", err)
	}
	err = s.walletSetup()
	if err != nil {
		return errors.Prefix("Initial wallet setup failed! Manual Intervention is required.", err)
	}
	allClaims, err := s.getClaims()
	if err != nil {
		return err
	}
	hasDupes, err := s.fixDupes(allClaims)
	if err != nil {
		return errors.Prefix("error checking for duplicates", err)
	}
	if hasDupes {
		SendInfoToSlack("Channel had dupes and was fixed!")
		err = s.waitForNewBlock()
		if err != nil {
			return err
		}
		allClaims, err = s.getClaims()
		if err != nil {
			return err
		}
	}

	pubsOnWallet, nFixed, nRemoved, err := s.updateRemoteDB(allClaims)
	if err != nil {
		return errors.Prefix("error updating remote database", err)
	}
	if nFixed > 0 || nRemoved > 0 {
		err := s.setStatusSyncing()
		if err != nil {
			return err
		}
		if nFixed > 0 {
			SendInfoToSlack("%d claims were not on the remote database and were fixed", nFixed)
		}
		if nRemoved > 0 {
			SendInfoToSlack("%d were marked as published but weren't actually published and thus removed from the database", nRemoved)
		}
	}
	pubsOnDB := 0
	for _, sv := range s.syncedVideos {
		if sv.Published {
			pubsOnDB++
		}
	}

	if pubsOnWallet > pubsOnDB { //This case should never happen
		SendInfoToSlack("We're claiming to have published %d videos but in reality we published %d (%s)", pubsOnDB, pubsOnWallet, s.YoutubeChannelID)
		return errors.Err("not all published videos are in the database")
	}
	if pubsOnWallet < pubsOnDB {
		SendInfoToSlack("we're claiming to have published %d videos but we only published %d (%s)", pubsOnDB, pubsOnWallet, s.YoutubeChannelID)
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
		err = errors.Err("UCB is not supported in this version of YTSYNC")
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
				logMsg := "error processing video: " + err.Error()
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
						"Unable to extract signature tokens",
						"Client.Timeout exceeded while awaiting headers)",
						"the video is too big to sync, skipping for now",
						"video is too long to process",
						"no compatible format available for this video",
						"Watch this video on YouTube.",
						"have blocked it on copyright grounds",
						"the video must be republished as we can't get the right size",
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
						} else if util.SubstringInSlice(err.Error(), []string{
							"Not enough funds to cover this transaction",
							"failed: Not enough funds",
							"Error in daemon: Insufficient funds, please deposit additional LBC"}) {
							log.Println("refilling addresses before retrying")
							err = s.walletSetup()
							if err != nil {
								s.grp.Stop()
								SendErrorToSlack("failed to setup the wallet for a refill: %v", err)
								break
							}
						} else if strings.Contains(err.Error(), "Error in daemon: 'str' object has no attribute 'get'") {
							time.Sleep(5 * time.Second)
						}
						log.Println("Retrying")
						continue
					}
					SendErrorToSlack("Video failed after %d retries, skipping. Stack: %s", tryCount, logMsg)
				}
				existingClaim, ok := s.syncedVideos[v.ID()]
				existingClaimID := ""
				existingClaimName := ""
				existingClaimSize := v.Size()
				if ok {
					existingClaimID = existingClaim.ClaimID
					existingClaimName = existingClaim.ClaimName
					if existingClaim.Size > 0 {
						existingClaimSize = &existingClaim.Size
					}
				}

				s.AppendSyncedVideo(v.ID(), false, err.Error(), "")
				err = s.Manager.apiConfig.MarkVideoStatus(s.YoutubeChannelID, v.ID(), VideoStatusFailed, existingClaimID, existingClaimName, err.Error(), existingClaimSize, 0)
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
		Transport: &transport.APIKey{Key: s.APIConfig.YoutubeAPIKey},
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
	playlistMap := make(map[string]*youtube.PlaylistItemSnippet, 50)
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
			// If there are 50+ videos in a playlist but less than 50 are actually returned by the API, youtube will still redirect
			// clients to a next page. Such next page will however be empty. This logic prevents ytsync from failing.
			youtubeIsLying := len(videos) > 0
			if youtubeIsLying {
				break
			}
			return errors.Err("playlist items not found")
		}
		//playlistMap := make(map[string]*youtube.PlaylistItemSnippet, 50)
		videoIDs := make([]string, 50)
		for i, item := range playlistResponse.Items {
			// normally we'd send the video into the channel here, but youtube api doesn't have sorting
			// so we have to get ALL the videos, then sort them, then send them in
			playlistMap[item.Snippet.ResourceId.VideoId] = item.Snippet
			videoIDs[i] = item.Snippet.ResourceId.VideoId
		}
		req2 := service.Videos.List("snippet,contentDetails,recordingDetails").Id(strings.Join(videoIDs[:], ","))

		videosListResponse, err := req2.Do()
		if err != nil {
			return errors.Prefix("error getting videos info", err)
		}
		for _, item := range videosListResponse.Items {
			videos = append(videos, sources.NewYoutubeVideo(s.videoDirectory, item, playlistMap[item.Id].Position, s.Manager.GetS3AWSConfig()))
		}

		log.Infof("Got info for %d videos from youtube API", len(videos))

		nextPageToken = playlistResponse.NextPageToken
		if nextPageToken == "" {
			break
		}
	}
	for k, v := range s.syncedVideos {
		if !v.Published {
			continue
		}
		_, ok := playlistMap[k]
		if !ok {
			videos = append(videos, sources.NewMockedVideo(s.videoDirectory, k, s.YoutubeChannelID, s.Manager.GetS3AWSConfig()))
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

func (s *Sync) processVideo(v video) (err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("stack: %s", debug.Stack())
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
	newMetadataVersion := int8(2)
	alreadyPublished := ok && sv.Published
	videoRequiresUpgrade := s.Manager.upgradeMetadata && sv.MetadataVersion < newMetadataVersion

	neverRetryFailures := []string{
		"Error extracting sts from embedded url response",
		"Unable to extract signature tokens",
		"the video is too big to sync, skipping for now",
		"video is too long to process",
		"no compatible format available for this video",
		"Watch this video on YouTube.",
		"have blocked it on copyright grounds",
	}
	if ok && !sv.Published && util.SubstringInSlice(sv.FailureReason, neverRetryFailures) {
		log.Println(v.ID() + " can't ever be published")
		return nil
	}

	if alreadyPublished && !videoRequiresUpgrade {
		log.Println(v.ID() + " already published")
		return nil
	}
	if ok && sv.MetadataVersion >= newMetadataVersion {
		log.Println(v.ID() + " upgraded to the new metadata")
		return nil
	}

	if v.PlaylistPosition() > s.Manager.videosLimit {
		log.Println(v.ID() + " is old: skipping")
		return nil
	}
	err = s.Manager.checkUsedSpace()
	if err != nil {
		return err
	}
	sp := sources.SyncParams{
		ClaimAddress:   s.claimAddress,
		Amount:         publishAmount,
		ChannelID:      s.lbryChannelID,
		MaxVideoSize:   s.Manager.maxVideoSize,
		Namer:          s.namer,
		MaxVideoLength: s.Manager.maxVideoLength,
		Fee:            s.Fee,
	}

	summary, err := v.Sync(s.daemon, sp, &sv, videoRequiresUpgrade)
	if err != nil {
		return err
	}

	err = s.Manager.apiConfig.MarkVideoStatus(s.YoutubeChannelID, v.ID(), VideoStatusPublished, summary.ClaimID, summary.ClaimName, "", v.Size(), 2)
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
		if p.Executable() == "lbrynet" {
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
