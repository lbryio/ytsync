package manager

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lbryio/ytsync/v5/ip_manager"
	"github.com/lbryio/ytsync/v5/namer"
	"github.com/lbryio/ytsync/v5/sdk"
	"github.com/lbryio/ytsync/v5/shared"
	"github.com/lbryio/ytsync/v5/sources"
	"github.com/lbryio/ytsync/v5/thumbs"
	"github.com/lbryio/ytsync/v5/timing"
	logUtils "github.com/lbryio/ytsync/v5/util"
	"github.com/lbryio/ytsync/v5/ytapi"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/stop"
	"github.com/lbryio/lbry.go/v2/extras/util"

	log "github.com/sirupsen/logrus"
)

const (
	channelClaimAmount    = 0.01
	estimatedMaxTxFee     = 0.1
	minimumAccountBalance = 1.0
	minimumRefillAmount   = 1
	publishAmount         = 0.01
	maxReasonLength       = 500
)

// Sync stores the options that control how syncing happens
type Sync struct {
	DbChannelData *shared.YoutubeChannel
	Manager       *SyncManager

	daemon           *jsonrpc.Client
	videoDirectory   string
	syncedVideosMux  *sync.RWMutex
	syncedVideos     map[string]sdk.SyncedVideo
	grp              *stop.Group
	namer            *namer.Namer
	walletMux        *sync.RWMutex
	queue            chan ytapi.Video
	defaultAccountID string
	hardVideoFailure hardVideoFailure
}

type hardVideoFailure struct {
	lock          *sync.Mutex
	failed        bool
	failureReason string
}

func (hv *hardVideoFailure) flagFailure(reason string) {
	hv.lock.Lock()
	defer hv.lock.Unlock()
	if hv.failed {
		return
	}
	hv.failed = true
	hv.failureReason = reason
}

func (s *Sync) AppendSyncedVideo(videoID string, published bool, failureReason string, claimName string, claimID string, metadataVersion int8, size int64) {
	s.syncedVideosMux.Lock()
	defer s.syncedVideosMux.Unlock()
	s.syncedVideos[videoID] = sdk.SyncedVideo{
		VideoID:         videoID,
		Published:       published,
		FailureReason:   failureReason,
		ClaimID:         claimID,
		ClaimName:       claimName,
		MetadataVersion: metadataVersion,
		Size:            size,
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

func (s *Sync) setStatusSyncing() error {
	syncedVideos, claimNames, err := s.Manager.ApiConfig.SetChannelStatus(s.DbChannelData.ChannelId, shared.StatusSyncing, "", nil)
	if err != nil {
		return err
	}
	s.syncedVideosMux.Lock()
	s.syncedVideos = syncedVideos
	s.namer.SetNames(claimNames)
	s.syncedVideosMux.Unlock()
	return nil
}

var stopGroup = stop.New()

func (s *Sync) FullCycle() (e error) {
	if os.Getenv("HOME") == "" {
		return errors.Err("no $HOME env var found")
	}
	defer timing.ClearTimings()
	s.syncedVideosMux = &sync.RWMutex{}
	s.walletMux = &sync.RWMutex{}
	s.grp = stopGroup
	s.queue = make(chan ytapi.Video)
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interruptChan)
	go func() {
		<-interruptChan
		util.SendToSlack("got interrupt, shutting down")
		log.Println("Got interrupt signal, shutting down (if publishing, will shut down after current publish)")
		s.grp.Stop()
		time.Sleep(5 * time.Second)
		debug.PrintStack() // so we can figure out what's not stopping
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
	err = s.downloadBlockchainDB()
	if err != nil {
		return errors.Prefix("failure in downloading blockchain.db", err)
	}

	defer s.stopAndUploadWallet(&e)

	s.videoDirectory, err = ioutil.TempDir(os.Getenv("TMP_DIR"), "ytsync")
	if err != nil {
		return errors.Wrap(err, 0)
	}
	err = os.Chmod(s.videoDirectory, 0766)
	if err != nil {
		return errors.Err(err)
	}

	defer deleteSyncFolder(s.videoDirectory)
	log.Printf("Starting daemon")
	err = logUtils.StartDaemon()
	if err != nil {
		return err
	}

	log.Infoln("Waiting for daemon to finish starting...")
	s.daemon = jsonrpc.NewClient(os.Getenv("LBRYNET_ADDRESS"))
	s.daemon.SetRPCTimeout(5 * time.Minute)

	err = s.waitForDaemonStart()
	if err != nil {
		return err
	}

	err = s.doSync()
	if err != nil {
		return err
	}

	if s.shouldTransfer() {
		err = s.processTransfers()
	}
	timing.Report()
	return err
}

func (s *Sync) processTransfers() (e error) {
	log.Println("Processing transfers")
	if s.DbChannelData.TransferState != 2 {
		err := waitConfirmations(s)
		if err != nil {
			return err
		}
	}
	supportAmount, err := abandonSupports(s)
	if err != nil {
		return errors.Prefix(fmt.Sprintf("%.6f LBCs were abandoned before failing", supportAmount), err)
	}
	if supportAmount > 0 {
		logUtils.SendInfoToSlack("(%s) %.6f LBCs were abandoned and should be used as support", s.DbChannelData.ChannelId, supportAmount)
	}
	err = transferVideos(s)
	if err != nil {
		return err
	}
	err = transferChannel(s)
	if err != nil {
		return err
	}
	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	reallocateSupports := supportAmount > 0.01
	if reallocateSupports {
		err = waitConfirmations(s)
		if err != nil {
			return err
		}
		isTip := true
		summary, err := s.daemon.SupportCreate(s.DbChannelData.ChannelClaimID, fmt.Sprintf("%.6f", supportAmount), &isTip, nil, []string{defaultAccount}, nil)
		if err != nil {
			if strings.Contains(err.Error(), "tx-size") { //TODO: this is a silly workaround...
				_, spendErr := s.daemon.TxoSpend(util.PtrToString("other"), nil, nil, nil, nil, &s.defaultAccountID)
				if spendErr != nil {
					return errors.Prefix(fmt.Sprintf("something went wrong while tipping the channel for %.6f LBCs", supportAmount), err)
				}
				err = s.waitForNewBlock()
				if err != nil {
					return errors.Prefix(fmt.Sprintf("something went wrong while tipping the channel for %.6f LBCs (waiting for new block)", supportAmount), err)
				}
				summary, err = s.daemon.SupportCreate(s.DbChannelData.ChannelClaimID, fmt.Sprintf("%.6f", supportAmount), &isTip, nil, []string{defaultAccount}, nil)
				if err != nil {
					return errors.Prefix(fmt.Sprintf("something went wrong while tipping the channel for %.6f LBCs", supportAmount), err)
				}
			} else {
				return errors.Prefix(fmt.Sprintf("something went wrong while tipping the channel for %.6f LBCs", supportAmount), err)
			}
		}
		if len(summary.Outputs) < 1 {
			return errors.Err("something went wrong while tipping the channel for %.6f LBCs", supportAmount)
		}
	}
	log.Println("Done processing transfers")
	return nil
}

func deleteSyncFolder(videoDirectory string) {
	if !strings.Contains(videoDirectory, "/tmp/ytsync") {
		_ = util.SendToSlack(errors.Err("Trying to delete an unexpected directory: %s", videoDirectory).Error())
	}
	err := os.RemoveAll(videoDirectory)
	if err != nil {
		_ = util.SendToSlack(err.Error())
	}
}

func (s *Sync) shouldTransfer() bool {
	return s.DbChannelData.TransferState >= 1 && s.DbChannelData.PublishAddress != "" && !s.Manager.CliFlags.DisableTransfers && s.DbChannelData.TransferState != 3
}

func (s *Sync) setChannelTerminationStatus(e *error) {
	var transferState *int

	if s.shouldTransfer() {
		if *e == nil {
			transferState = util.PtrToInt(shared.TransferStateComplete)
		}
	}
	if *e != nil {
		//conditions for which a channel shouldn't be marked as failed
		noFailConditions := []string{
			"this youtube channel is being managed by another server",
			"interrupted during daemon startup",
			"interrupted by user",
			"use --skip-space-check to ignore",
		}
		dbWipeConditions := []string{
			"Missing inputs",
		}
		if util.SubstringInSlice((*e).Error(), noFailConditions) {
			return
		}
		channelStatus := shared.StatusFailed
		if util.SubstringInSlice((*e).Error(), dbWipeConditions) {
			channelStatus = shared.StatusWipeDb
		}
		failureReason := (*e).Error()
		_, _, err := s.Manager.ApiConfig.SetChannelStatus(s.DbChannelData.ChannelId, channelStatus, failureReason, transferState)
		if err != nil {
			msg := fmt.Sprintf("Failed setting failed state for channel %s", s.DbChannelData.DesiredChannelName)
			*e = errors.Prefix(msg+err.Error(), *e)
		}
	} else if !s.IsInterrupted() {
		_, _, err := s.Manager.ApiConfig.SetChannelStatus(s.DbChannelData.ChannelId, shared.StatusSynced, "", transferState)
		if err != nil {
			*e = err
		}
	}
}

func (s *Sync) waitForDaemonStart() error {
	beginTime := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("waitForDaemonStart").Add(time.Since(start))
	}(beginTime)
	for {
		select {
		case <-s.grp.Ch():
			return errors.Err("interrupted during daemon startup")
		default:
			status, err := s.daemon.Status()
			if err == nil && status.StartupStatus.Wallet && status.IsRunning {
				return nil
			}
			if time.Since(beginTime).Minutes() > 120 {
				s.grp.Stop()
				return errors.Err("the daemon is taking too long to start. Something is wrong")
			}
			time.Sleep(5 * time.Second)
		}
	}
}

func (s *Sync) stopAndUploadWallet(e *error) {
	log.Printf("Stopping daemon")
	shutdownErr := logUtils.StopDaemon()
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
				} else {
					*e = errors.Prefix("failure uploading wallet", *e)
				}
			}
			err = s.uploadBlockchainDB()
			if err != nil {
				if *e == nil {
					e = &err
				} else {
					*e = errors.Prefix("failure uploading wallet", *e)
				}
			}
		}
	}
}
func logShutdownError(shutdownErr error) {
	logUtils.SendErrorToSlack("error shutting down daemon: %s", errors.FullTrace(shutdownErr))
	logUtils.SendErrorToSlack("WALLET HAS NOT BEEN MOVED TO THE WALLET BACKUP DIR")
}

var thumbnailHosts = []string{
	"berk.ninja/thumbnails/",
	thumbs.ThumbnailEndpoint,
}

func isYtsyncClaim(c jsonrpc.Claim, expectedChannelID string) bool {
	if !util.InSlice(c.Type, []string{"claim", "update"}) || c.Value.GetStream() == nil {
		return false
	}
	if c.Value.GetThumbnail() == nil || c.Value.GetThumbnail().GetUrl() == "" {
		//most likely a claim created outside of ytsync, ignore!
		return false
	}
	if c.SigningChannel == nil {
		return false
	}
	if c.SigningChannel.ClaimID != expectedChannelID {
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
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("fixDupes").Add(time.Since(start))
	}(start)
	abandonedClaims := false
	videoIDs := make(map[string]jsonrpc.Claim)
	for _, c := range claims {
		if !isYtsyncClaim(c, s.DbChannelData.ChannelClaimID) {
			continue
		}
		tn := c.Value.GetThumbnail().GetUrl()
		videoID := tn[strings.LastIndex(tn, "/")+1:]

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
		if claimToAbandon.Address != s.DbChannelData.PublishAddress && !s.syncedVideos[videoID].Transferred {
			log.Debugf("abandoning %+v", claimToAbandon)
			_, err := s.daemon.StreamAbandon(claimToAbandon.Txid, claimToAbandon.Nout, nil, true)
			if err != nil {
				return true, err
			}
			abandonedClaims = true
		} else {
			log.Debugf("lbrynet stream abandon --txid=%s --nout=%d", claimToAbandon.Txid, claimToAbandon.Nout)
		}
	}
	return abandonedClaims, nil
}

type ytsyncClaim struct {
	ClaimID         string
	MetadataVersion uint
	ClaimName       string
	PublishAddress  string
	VideoID         string
	Claim           *jsonrpc.Claim
}

// mapFromClaims returns a map of videoIDs (youtube id) to ytsyncClaim which is a structure holding blockchain related
// information
func (s *Sync) mapFromClaims(claims []jsonrpc.Claim) map[string]ytsyncClaim {
	videoIDMap := make(map[string]ytsyncClaim, len(claims))
	for _, c := range claims {
		if !isYtsyncClaim(c, s.DbChannelData.ChannelClaimID) {
			continue
		}
		tn := c.Value.GetThumbnail().GetUrl()
		videoID := tn[strings.LastIndex(tn, "/")+1:]
		claimMetadataVersion := uint(1)
		if strings.Contains(tn, thumbs.ThumbnailEndpoint) {
			claimMetadataVersion = 2
		}

		videoIDMap[videoID] = ytsyncClaim{
			ClaimID:         c.ClaimID,
			MetadataVersion: claimMetadataVersion,
			ClaimName:       c.Name,
			PublishAddress:  c.Address,
			VideoID:         videoID,
			Claim:           &c,
		}
	}
	return videoIDMap
}

//updateRemoteDB counts the amount of videos published so far and updates the remote db if some videos weren't marked as published
//additionally it removes all entries in the database indicating that a video is published when it's actually not
func (s *Sync) updateRemoteDB(claims []jsonrpc.Claim, ownClaims []jsonrpc.Claim) (total, fixed, removed int, err error) {
	allClaimsInfo := s.mapFromClaims(claims)
	ownClaimsInfo := s.mapFromClaims(ownClaims)
	count := len(allClaimsInfo)
	idsToRemove := make([]string, 0, count)

	for videoID, chainInfo := range allClaimsInfo {
		s.syncedVideosMux.RLock()
		sv, claimInDatabase := s.syncedVideos[videoID]
		s.syncedVideosMux.RUnlock()

		metadataDiffers := claimInDatabase && sv.MetadataVersion != int8(chainInfo.MetadataVersion)
		claimIDDiffers := claimInDatabase && sv.ClaimID != chainInfo.ClaimID
		claimNameDiffers := claimInDatabase && sv.ClaimName != chainInfo.ClaimName
		claimMarkedUnpublished := claimInDatabase && !sv.Published
		_, isOwnClaim := ownClaimsInfo[videoID]
		transferred := !isOwnClaim || s.DbChannelData.TransferState == 3
		transferStatusMismatch := sv.Transferred != transferred

		if metadataDiffers {
			log.Debugf("%s: Mismatch in database for metadata. DB: %d - Blockchain: %d", videoID, sv.MetadataVersion, chainInfo.MetadataVersion)
		}
		if claimIDDiffers {
			log.Debugf("%s: Mismatch in database for claimID. DB: %s - Blockchain: %s", videoID, sv.ClaimID, chainInfo.ClaimID)
		}
		if claimNameDiffers {
			log.Debugf("%s: Mismatch in database for claimName. DB: %s - Blockchain: %s", videoID, sv.ClaimName, chainInfo.ClaimName)
		}
		if claimMarkedUnpublished {
			log.Debugf("%s: Mismatch in database: published but marked as unpublished", videoID)
		}
		if !claimInDatabase {
			log.Debugf("%s: Published but is not in database (%s - %s)", videoID, chainInfo.ClaimName, chainInfo.ClaimID)
		}
		if transferStatusMismatch {
			log.Debugf("%s: is marked as transferred %t on it's actually %t", videoID, sv.Transferred, transferred)
		}

		if !claimInDatabase || metadataDiffers || claimIDDiffers || claimNameDiffers || claimMarkedUnpublished || transferStatusMismatch {
			claimSize, err := chainInfo.Claim.GetStreamSizeByMagic()
			if err != nil {
				claimSize = 0
			}
			fixed++
			log.Debugf("updating %s in the database", videoID)
			err = s.Manager.ApiConfig.MarkVideoStatus(shared.VideoStatus{
				ChannelID:       s.DbChannelData.ChannelId,
				VideoID:         videoID,
				Status:          shared.VideoStatusPublished,
				ClaimID:         chainInfo.ClaimID,
				ClaimName:       chainInfo.ClaimName,
				Size:            util.PtrToInt64(int64(claimSize)),
				MetaDataVersion: chainInfo.MetadataVersion,
				IsTransferred:   &transferred,
			})
			if err != nil {
				return count, fixed, 0, err
			}
		}
	}

	//reload the synced videos map before we use it for further processing
	if fixed > 0 {
		err := s.setStatusSyncing()
		if err != nil {
			return count, fixed, 0, err
		}
	}

	for vID, sv := range s.syncedVideos {
		if sv.Transferred || sv.IsLbryFirst {
			_, ok := allClaimsInfo[vID]
			if !ok && sv.Published {
				searchResponse, err := s.daemon.ClaimSearch(nil, &sv.ClaimID, nil, nil, 1, 20)
				if err != nil {
					log.Error(err.Error())
					continue
				}
				if len(searchResponse.Claims) == 0 {
					log.Debugf("%s: was transferred but appears abandoned! we should ignore this - claimID: %s", vID, sv.ClaimID)
					continue //TODO: we should flag these on the db
				} else {
					if sv.IsLbryFirst {
						log.Debugf("%s: was published using lbry-first so we don't want to do anything here! - claimID: %s", vID, sv.ClaimID)
					} else {
						log.Debugf("%s: was transferred and was then edited! we should ignore this - claimID: %s", vID, sv.ClaimID)
					}
					//return count, fixed, 0, errors.Err("%s: isn't our control but is on the database and on the blockchain. wtf is up? ClaimID: %s", vID, sv.ClaimID)
				}
			}
			continue
		}
		_, ok := ownClaimsInfo[vID]
		if !ok && sv.Published {
			log.Debugf("%s: claims to be published but wasn't found in the list of claims and will be removed if --remove-db-unpublished was specified (%t)", vID, s.Manager.CliFlags.RemoveDBUnpublished)
			idsToRemove = append(idsToRemove, vID)
		}
	}
	if s.Manager.CliFlags.RemoveDBUnpublished && len(idsToRemove) > 0 {
		log.Infof("removing: %s", strings.Join(idsToRemove, ","))
		err := s.Manager.ApiConfig.DeleteVideos(idsToRemove)
		if err != nil {
			return count, fixed, len(idsToRemove), err
		}
		removed++
	}
	//reload the synced videos map before we use it for further processing
	if removed > 0 {
		err := s.setStatusSyncing()
		if err != nil {
			return count, fixed, removed, err
		}
	}
	return count, fixed, removed, nil
}

func (s *Sync) getClaims(defaultOnly bool) ([]jsonrpc.Claim, error) {
	var account *string = nil
	if defaultOnly {
		a, err := s.getDefaultAccount()
		if err != nil {
			return nil, err
		}
		account = &a
	}
	claims, err := s.daemon.StreamList(account, 1, 30000)
	if err != nil {
		return nil, errors.Prefix("cannot list claims", err)
	}
	items := make([]jsonrpc.Claim, 0, len(claims.Items))
	for _, c := range claims.Items {
		if c.SigningChannel != nil && c.SigningChannel.ClaimID == s.DbChannelData.ChannelClaimID {
			items = append(items, c)
		}
	}
	return items, nil
}

func (s *Sync) checkIntegrity() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("checkIntegrity").Add(time.Since(start))
	}(start)
	allClaims, err := s.getClaims(false)
	if err != nil {
		return err
	}
	hasDupes, err := s.fixDupes(allClaims)
	if err != nil {
		return errors.Prefix("error checking for duplicates", err)
	}
	if hasDupes {
		logUtils.SendInfoToSlack("Channel had dupes and was fixed!")
		err = s.waitForNewBlock()
		if err != nil {
			return err
		}
		allClaims, err = s.getClaims(false)
		if err != nil {
			return err
		}
	}

	ownClaims, err := s.getClaims(true)
	if err != nil {
		return err
	}
	pubsOnWallet, nFixed, nRemoved, err := s.updateRemoteDB(allClaims, ownClaims)
	if err != nil {
		return errors.Prefix("error updating remote database", err)
	}

	if nFixed > 0 || nRemoved > 0 {
		if nFixed > 0 {
			logUtils.SendInfoToSlack("%d claims had mismatched database info or were completely missing and were fixed", nFixed)
		}
		if nRemoved > 0 {
			logUtils.SendInfoToSlack("%d were marked as published but weren't actually published and thus removed from the database", nRemoved)
		}
	}
	pubsOnDB := 0
	for _, sv := range s.syncedVideos {
		if sv.Published {
			pubsOnDB++
		}
	}

	if pubsOnWallet > pubsOnDB { //This case should never happen
		logUtils.SendInfoToSlack("We're claiming to have published %d videos but in reality we published %d (%s)", pubsOnDB, pubsOnWallet, s.DbChannelData.ChannelId)
		return errors.Err("not all published videos are in the database")
	}
	if pubsOnWallet < pubsOnDB {
		logUtils.SendInfoToSlack("we're claiming to have published %d videos but we only published %d (%s)", pubsOnDB, pubsOnWallet, s.DbChannelData.ChannelId)
	}

	_, err = s.getUnsentSupports() //TODO: use the returned value when it works
	if err != nil {
		return err
	}
	return nil
}

func (s *Sync) doSync() error {
	err := s.enableAddressReuse()
	if err != nil {
		return errors.Prefix("could not set address reuse policy", err)
	}
	err = s.importPublicKey()
	if err != nil {
		return errors.Prefix("could not import the transferee public key", err)
	}
	_, err = s.daemon.UTXORelease(nil)
	if err != nil {
		return errors.Prefix("could not run uxo_release", err)
	}
	err = s.walletSetup()
	if err != nil {
		return errors.Prefix("Initial wallet setup failed! Manual Intervention is required.", err)
	}

	err = s.checkIntegrity()
	if err != nil {
		return err
	}

	if s.DbChannelData.TransferState < shared.TransferStateComplete {
		cert, err := s.daemon.ChannelExport(s.DbChannelData.ChannelClaimID, nil, nil)
		if err != nil {
			return errors.Prefix("error getting channel cert", err)
		}
		if cert != nil {
			err = s.Manager.ApiConfig.SetChannelCert(string(*cert), s.DbChannelData.ChannelClaimID)
			if err != nil {
				return errors.Prefix("error setting channel cert", err)
			}
		}
	}

	for i := 0; i < s.Manager.CliFlags.ConcurrentJobs; i++ {
		s.grp.Add(1)
		go func(i int) {
			defer s.grp.Done()
			s.startWorker(i)
		}(i)
	}

	if s.DbChannelData.DesiredChannelName == "@UCBerkeley" {
		err = errors.Err("UCB is not supported in this version of YTSYNC")
	} else {
		err = s.enqueueYoutubeVideos()
	}
	close(s.queue)
	s.grp.Wait()
	if err != nil {
		return err
	}
	if s.hardVideoFailure.failed {
		return errors.Err(s.hardVideoFailure.failureReason)
	}
	return nil
}

func (s *Sync) startWorker(workerNum int) {
	var v ytapi.Video
	var more bool
	fatalErrors := []string{
		":5279: read: connection reset by peer",
		"no space left on device",
		"NotEnoughFunds",
		"Cannot publish using channel",
		"cannot concatenate 'str' and 'NoneType' objects",
		"more than 90% of the space has been used.",
		"Couldn't find private key for id",
		"You already have a stream claim published under the name",
		"Missing inputs",
	}
	errorsNoRetry := []string{
		"non 200 status code received",
		"This video contains content from",
		"dont know which claim to update",
		"uploader has not made this video available in your country",
		"download error: AccessDenied: Access Denied",
		"Playback on other websites has been disabled by the video owner",
		"Error in daemon: Cannot publish empty file",
		"Error extracting sts from embedded url response",
		"Unable to extract signature tokens",
		"Client.Timeout exceeded while awaiting headers",
		"the video is too big to sync, skipping for now",
		"video is too long to process",
		"video is too short to process",
		"no compatible format available for this video",
		"Watch this video on YouTube.",
		"have blocked it on copyright grounds",
		"the video must be republished as we can't get the right size",
		"HTTP Error 403",
		"giving up after 0 fragment retries",
		"Sorry about that",
		"This video is not available",
		"requested format not available",
		"interrupted by user",
		"Sign in to confirm your age",
		"This video is unavailable",
		"video is a live stream and hasn't completed yet",
	}
	walletErrors := []string{
		"Not enough funds to cover this transaction",
		"failed: Not enough funds",
		"Error in daemon: Insufficient funds, please deposit additional LBC",
		//"Missing inputs",
	}
	blockchainErrors := []string{
		"txn-mempool-conflict",
		"too-long-mempool-chain",
	}
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
			select { // check again inside the loop so this dies faster
			case <-s.grp.Ch():
				log.Printf("Stopping worker %d", workerNum)
				return
			default:
			}
			tryCount++
			err := s.processVideo(v)
			if err != nil {
				logUtils.SendErrorToSlack("error processing video %s: %s", v.ID(), err.Error())
				shouldRetry := s.Manager.CliFlags.MaxTries > 1 && !util.SubstringInSlice(err.Error(), errorsNoRetry) && tryCount < s.Manager.CliFlags.MaxTries
				if strings.Contains(strings.ToLower(err.Error()), "interrupted by user") {
					s.grp.Stop()
				} else if util.SubstringInSlice(err.Error(), fatalErrors) {
					s.hardVideoFailure.flagFailure(err.Error())
					s.grp.Stop()
				} else if shouldRetry {
					if util.SubstringInSlice(err.Error(), blockchainErrors) {
						log.Println("waiting for a block before retrying")
						err := s.waitForNewBlock()
						if err != nil {
							s.grp.Stop()
							logUtils.SendErrorToSlack("something went wrong while waiting for a block: %s", errors.FullTrace(err))
							break
						}
					} else if util.SubstringInSlice(err.Error(), walletErrors) {
						log.Println("checking funds and UTXOs before retrying...")
						err := s.walletSetup()
						if err != nil {
							s.grp.Stop()
							logUtils.SendErrorToSlack("failed to setup the wallet for a refill: %s", errors.FullTrace(err))
							break
						}
					} else if strings.Contains(err.Error(), "Error in daemon: 'str' object has no attribute 'get'") {
						time.Sleep(5 * time.Second)
					}
					log.Println("Retrying")
					continue
				}
				logUtils.SendErrorToSlack("Video %s failed after %d retries, skipping. Stack: %s", v.ID(), tryCount, errors.FullTrace(err))

				s.syncedVideosMux.RLock()
				existingClaim, ok := s.syncedVideos[v.ID()]
				s.syncedVideosMux.RUnlock()
				existingClaimID := ""
				existingClaimName := ""
				existingClaimSize := int64(0)
				if v.Size() != nil {
					existingClaimSize = *v.Size()
				}
				if ok {
					existingClaimID = existingClaim.ClaimID
					existingClaimName = existingClaim.ClaimName
					if existingClaim.Size > 0 {
						existingClaimSize = existingClaim.Size
					}
				}
				videoStatus := shared.VideoStatusFailed
				if strings.Contains(err.Error(), "upgrade failed") {
					videoStatus = shared.VideoStatusUpgradeFailed
				} else {
					s.AppendSyncedVideo(v.ID(), false, err.Error(), existingClaimName, existingClaimID, 0, existingClaimSize)
				}
				err = s.Manager.ApiConfig.MarkVideoStatus(shared.VideoStatus{
					ChannelID:     s.DbChannelData.ChannelId,
					VideoID:       v.ID(),
					Status:        videoStatus,
					ClaimID:       existingClaimID,
					ClaimName:     existingClaimName,
					FailureReason: err.Error(),
					Size:          &existingClaimSize,
				})
				if err != nil {
					logUtils.SendErrorToSlack("Failed to mark video on the database: %s", errors.FullTrace(err))
				}
			}
			break
		}
	}
}

func (s *Sync) enqueueYoutubeVideos() error {
	defer func(start time.Time) { timing.TimedComponent("enqueueYoutubeVideos").Add(time.Since(start)) }(time.Now())

	ipPool, err := ip_manager.GetIPPool(s.grp)
	if err != nil {
		return err
	}

	videos, err := ytapi.GetVideosToSync(s.Manager.ApiConfig, s.DbChannelData.ChannelId, s.syncedVideos, s.Manager.CliFlags.QuickSync, s.Manager.CliFlags.VideosToSync(s.DbChannelData.TotalSubscribers), ytapi.VideoParams{
		VideoDir: s.videoDirectory,
		S3Config: *s.Manager.AwsConfigs.GetS3AWSConfig(),
		Stopper:  s.grp,
		IPPool:   ipPool,
	}, s.DbChannelData.LastUploadedVideo)
	if err != nil {
		return err
	}

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

func (s *Sync) processVideo(v ytapi.Video) (err error) {
	defer func() {
		if p := recover(); p != nil {
			logUtils.SendErrorToSlack("Video processing panic! %s", debug.Stack())
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
	videoRequiresUpgrade := ok && s.Manager.CliFlags.UpgradeMetadata && sv.MetadataVersion < newMetadataVersion

	neverRetryFailures := shared.NeverRetryFailures
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

	if !videoRequiresUpgrade && v.PlaylistPosition() >= s.Manager.CliFlags.VideosToSync(s.DbChannelData.TotalSubscribers) {
		log.Println(v.ID() + " is old: skipping")
		return nil
	}
	err = s.Manager.checkUsedSpace()
	if err != nil {
		return err
	}
	da, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	sp := sources.SyncParams{
		ClaimAddress:   s.DbChannelData.PublishAddress,
		Amount:         publishAmount,
		ChannelID:      s.DbChannelData.ChannelClaimID,
		MaxVideoSize:   s.DbChannelData.SizeLimit,
		Namer:          s.namer,
		MaxVideoLength: time.Duration(s.DbChannelData.LengthLimit) * time.Minute,
		Fee:            s.DbChannelData.Fee,
		DefaultAccount: da,
	}

	summary, err := v.Sync(s.daemon, sp, &sv, videoRequiresUpgrade, s.walletMux)
	if err != nil {
		return err
	}

	s.AppendSyncedVideo(v.ID(), true, "", summary.ClaimName, summary.ClaimID, newMetadataVersion, *v.Size())
	err = s.Manager.ApiConfig.MarkVideoStatus(shared.VideoStatus{
		ChannelID:       s.DbChannelData.ChannelId,
		VideoID:         v.ID(),
		Status:          shared.VideoStatusPublished,
		ClaimID:         summary.ClaimID,
		ClaimName:       summary.ClaimName,
		Size:            v.Size(),
		MetaDataVersion: shared.LatestMetadataVersion,
		IsTransferred:   util.PtrToBool(s.shouldTransfer()),
	})
	if err != nil {
		logUtils.SendErrorToSlack("Failed to mark video on the database: %s", errors.FullTrace(err))
	}

	return nil
}

func (s *Sync) importPublicKey() error {
	if s.DbChannelData.PublicKey != "" {
		accountsResponse, err := s.daemon.AccountList(1, 50)
		if err != nil {
			return errors.Err(err)
		}
		ledger := "lbc_mainnet"
		if logUtils.IsRegTest() {
			ledger = "lbc_regtest"
		}
		for _, a := range accountsResponse.Items {
			if *a.Ledger == ledger {
				if a.PublicKey == s.DbChannelData.PublicKey {
					return nil
				}
			}
		}
		log.Infof("Could not find public key %s in the wallet. Importing it...", s.DbChannelData.PublicKey)
		_, err = s.daemon.AccountAdd(s.DbChannelData.DesiredChannelName, nil, nil, &s.DbChannelData.PublicKey, util.PtrToBool(true), nil)
		return errors.Err(err)
	}
	return nil
}

//TODO: fully implement this once I find a way to reliably get the abandoned supports amount
func (s *Sync) getUnsentSupports() (float64, error) {
	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return 0, errors.Err(err)
	}
	if s.DbChannelData.TransferState == 2 {
		balance, err := s.daemon.AccountBalance(&defaultAccount)
		if err != nil {
			return 0, err
		} else if balance == nil {
			return 0, errors.Err("no response")
		}

		balanceAmount, err := strconv.ParseFloat(balance.Available.String(), 64)
		if err != nil {
			return 0, errors.Err(err)
		}
		transactionList, err := s.daemon.TransactionList(&defaultAccount, nil, 1, 90000)
		if err != nil {
			return 0, errors.Err(err)
		}
		sentSupports := 0.0
		for _, t := range transactionList.Items {
			if len(t.SupportInfo) == 0 {
				continue
			}
			for _, support := range t.SupportInfo {
				supportAmount, err := strconv.ParseFloat(support.BalanceDelta, 64)
				if err != nil {
					return 0, err
				}
				if supportAmount < 0 { // && support.IsTip TODO: re-enable this when transaction list shows correct information
					sentSupports += -supportAmount
				}
			}
		}
		if balanceAmount > 10 && sentSupports < 1 && s.DbChannelData.TransferState > 1 {
			logUtils.SendErrorToSlack("(%s) this channel has quite some LBCs in it (%.2f) and %.2f LBC in sent tips, it's likely that the tips weren't actually sent or the wallet has unnecessary extra credits in it", s.DbChannelData.ChannelId, balanceAmount, sentSupports)
			return balanceAmount - 10, nil
		}
	}
	return 0, nil
}

// waitForDaemonProcess observes the running processes and returns when the process is no longer running or when the timeout is up
func waitForDaemonProcess(timeout time.Duration) error {
	stopTime := time.Now().Add(timeout * time.Second)
	for !time.Now().After(stopTime) {
		wait := 10 * time.Second
		log.Println("the daemon is still running, waiting for it to exit")
		time.Sleep(wait)
		running, err := logUtils.IsLbrynetRunning()
		if err != nil {
			return errors.Err(err)
		}
		if !running {
			log.Println("daemon stopped")
			return nil
		}
	}
	return errors.Err("timeout reached")
}
