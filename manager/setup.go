package manager

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/util"
	"github.com/lbryio/ytsync/v5/shared"
	"github.com/lbryio/ytsync/v5/timing"
	logUtils "github.com/lbryio/ytsync/v5/util"
	"github.com/lbryio/ytsync/v5/ytapi"

	"github.com/lbryio/ytsync/v5/tags_manager"
	"github.com/lbryio/ytsync/v5/thumbs"

	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

func (s *Sync) enableAddressReuse() error {
	accountsResponse, err := s.daemon.AccountList(1, 50)
	if err != nil {
		return errors.Err(err)
	}
	accounts := make([]jsonrpc.Account, 0, len(accountsResponse.Items))
	ledger := "lbc_mainnet"
	if logUtils.IsRegTest() {
		ledger = "lbc_regtest"
	}
	for _, a := range accountsResponse.Items {
		if *a.Ledger == ledger {
			accounts = append(accounts, a)
		}
	}

	for _, a := range accounts {
		_, err = s.daemon.AccountSet(a.ID, jsonrpc.AccountSettings{
			ChangeMaxUses:    util.PtrToInt(1000),
			ReceivingMaxUses: util.PtrToInt(100),
		})
		if err != nil {
			return errors.Err(err)
		}
	}
	return nil
}
func (s *Sync) walletSetup() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("walletSetup").Add(time.Since(start))
	}(start)
	//prevent unnecessary concurrent execution and publishing while refilling/reallocating UTXOs
	s.walletMux.Lock()
	defer s.walletMux.Unlock()
	err := s.ensureChannelOwnership()
	if err != nil {
		return err
	}

	balanceResp, err := s.daemon.AccountBalance(nil)
	if err != nil {
		return err
	} else if balanceResp == nil {
		return errors.Err("no response")
	}
	balance, err := strconv.ParseFloat(balanceResp.Available.String(), 64)
	if err != nil {
		return errors.Err(err)
	}
	log.Debugf("Starting balance is %.4f", balance)

	videosOnYoutube, err := ytapi.CountVideosInChannel(s.DbChannelData.ChannelId)
	if err != nil {
		return err
	}

	log.Debugf("Source channel has %d videos", videosOnYoutube)
	if videosOnYoutube == 0 {
		return nil
	}

	s.syncedVideosMux.RLock()
	publishedCount := 0
	notUpgradedCount := 0
	failedCount := 0
	for _, sv := range s.syncedVideos {
		if sv.Published {
			publishedCount++
			if sv.MetadataVersion < 2 {
				notUpgradedCount++
			}
		} else {
			failedCount++
		}
	}
	s.syncedVideosMux.RUnlock()

	log.Debugf("We already allocated credits for %d published videos and %d failed videos", publishedCount, failedCount)

	if videosOnYoutube > s.Manager.CliFlags.VideosLimit {
		videosOnYoutube = s.Manager.CliFlags.VideosLimit
	}
	unallocatedVideos := videosOnYoutube - (publishedCount + failedCount)
	channelFee := channelClaimAmount
	channelAlreadyClaimed := s.DbChannelData.ChannelClaimID != ""
	if channelAlreadyClaimed {
		channelFee = 0.0
	}
	requiredBalance := float64(unallocatedVideos)*(publishAmount+estimatedMaxTxFee) + channelFee
	if s.Manager.CliFlags.UpgradeMetadata {
		requiredBalance += float64(notUpgradedCount) * 0.001
	}

	refillAmount := 0.0
	if balance < requiredBalance || balance < minimumAccountBalance {
		refillAmount = math.Max(math.Max(requiredBalance-balance, minimumAccountBalance-balance), minimumRefillAmount)
	}

	if s.Manager.CliFlags.Refill > 0 {
		refillAmount += float64(s.Manager.CliFlags.Refill)
	}

	if refillAmount > 0 {
		err := s.addCredits(refillAmount)
		if err != nil {
			return errors.Err(err)
		}
	}

	claimAddress, err := s.daemon.AddressList(nil, nil, 1, 20)
	if err != nil {
		return err
	} else if claimAddress == nil {
		return errors.Err("could not get an address")
	}
	if s.DbChannelData.PublishAddress == "" || !s.shouldTransfer() {
		s.DbChannelData.PublishAddress = string(claimAddress.Items[0].Address)
	}
	if s.DbChannelData.PublishAddress == "" {
		return errors.Err("found blank claim address")
	}

	err = s.ensureEnoughUTXOs()
	if err != nil {
		return err
	}

	return nil
}

func (s *Sync) getDefaultAccount() (string, error) {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("getDefaultAccount").Add(time.Since(start))
	}(start)
	if s.defaultAccountID == "" {
		accountsResponse, err := s.daemon.AccountList(1, 50)
		if err != nil {
			return "", errors.Err(err)
		}
		ledger := "lbc_mainnet"
		if logUtils.IsRegTest() {
			ledger = "lbc_regtest"
		}
		for _, a := range accountsResponse.Items {
			if *a.Ledger == ledger {
				if a.IsDefault {
					s.defaultAccountID = a.ID
					break
				}
			}
		}

		if s.defaultAccountID == "" {
			return "", errors.Err("No default account found")
		}
	}
	return s.defaultAccountID, nil
}

func (s *Sync) ensureEnoughUTXOs() error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("ensureEnoughUTXOs").Add(time.Since(start))
	}(start)
	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return err
	}

	utxolist, err := s.daemon.UTXOList(&defaultAccount, 1, 10000)
	if err != nil {
		return err
	} else if utxolist == nil {
		return errors.Err("no response")
	}

	target := 40
	slack := int(float32(0.1) * float32(target))
	count := 0
	confirmedCount := 0

	for _, utxo := range utxolist.Items {
		amount, _ := strconv.ParseFloat(utxo.Amount, 64)
		if utxo.IsMyOutput && utxo.Type == "payment" && amount > 0.001 {
			if utxo.Confirmations > 0 {
				confirmedCount++
			}
			count++
		}
	}
	log.Infof("utxo count: %d (%d confirmed)", count, confirmedCount)
	UTXOWaitThreshold := 16
	if count < target-slack {
		balance, err := s.daemon.AccountBalance(&defaultAccount)
		if err != nil {
			return err
		} else if balance == nil {
			return errors.Err("no response")
		}

		balanceAmount, err := strconv.ParseFloat(balance.Available.String(), 64)
		if err != nil {
			return errors.Err(err)
		}
		maxUTXOs := uint64(500)
		desiredUTXOCount := uint64(math.Floor((balanceAmount) / 0.1))
		if desiredUTXOCount > maxUTXOs {
			desiredUTXOCount = maxUTXOs
		}
		if desiredUTXOCount < uint64(confirmedCount) {
			return nil
		}
		availableBalance, _ := balance.Available.Float64()
		log.Infof("Splitting balance of %.3f evenly between %d UTXOs", availableBalance, desiredUTXOCount)

		broadcastFee := 0.1
		prefillTx, err := s.daemon.AccountFund(defaultAccount, defaultAccount, fmt.Sprintf("%.4f", balanceAmount-broadcastFee), desiredUTXOCount, false)
		if err != nil {
			return err
		} else if prefillTx == nil {
			return errors.Err("no response")
		}
		if confirmedCount < UTXOWaitThreshold {
			err = s.waitForNewBlock()
			if err != nil {
				return err
			}
		}
	} else if confirmedCount < UTXOWaitThreshold {
		log.Println("Waiting for previous txns to confirm")
		err := s.waitForNewBlock()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Sync) waitForNewBlock() error {
	defer func(start time.Time) { timing.TimedComponent("waitForNewBlock").Add(time.Since(start)) }(time.Now())

	log.Printf("regtest: %t, docker: %t", logUtils.IsRegTest(), logUtils.IsUsingDocker())
	status, err := s.daemon.Status()
	if err != nil {
		return err
	}

	for status.Wallet.Blocks == 0 || status.Wallet.BlocksBehind != 0 {
		time.Sleep(5 * time.Second)
		status, err = s.daemon.Status()
		if err != nil {
			return err
		}
	}

	currentBlock := status.Wallet.Blocks
	for i := 0; status.Wallet.Blocks <= currentBlock; i++ {
		if i%3 == 0 {
			log.Printf("Waiting for new block (%d)...", currentBlock+1)
		}
		if logUtils.IsRegTest() && logUtils.IsUsingDocker() {
			err = s.GenerateRegtestBlock()
			if err != nil {
				return err
			}
		}
		time.Sleep(10 * time.Second)
		status, err = s.daemon.Status()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Sync) GenerateRegtestBlock() error {
	lbrycrd, err := logUtils.GetLbrycrdClient(s.Manager.LbrycrdDsn)
	if err != nil {
		return errors.Prefix("error getting lbrycrd client: ", err)
	}

	txs, err := lbrycrd.Generate(1)
	if err != nil {
		return errors.Prefix("error generating new block: ", err)
	}

	for _, tx := range txs {
		log.Info("Generated tx: ", tx.String())
	}
	return nil
}

func (s *Sync) ensureChannelOwnership() error {
	defer func(start time.Time) { timing.TimedComponent("ensureChannelOwnership").Add(time.Since(start)) }(time.Now())

	if s.DbChannelData.DesiredChannelName == "" {
		return errors.Err("no channel name set")
	}

	channels, err := s.daemon.ChannelList(nil, 1, 50, nil)
	if err != nil {
		return err
	} else if channels == nil {
		return errors.Err("no channel response")
	}

	var channelToUse *jsonrpc.Transaction
	if len((*channels).Items) > 0 {
		if s.DbChannelData.ChannelClaimID == "" {
			return errors.Err("this channel does not have a recorded claimID in the database. To prevent failures, updates are not supported until an entry is manually added in the database")
		}
		for _, c := range (*channels).Items {
			log.Debugf("checking listed channel %s (%s)", c.ClaimID, c.Name)
			if c.ClaimID != s.DbChannelData.ChannelClaimID {
				continue
			}
			if c.Name != s.DbChannelData.DesiredChannelName {
				return errors.Err("the channel in the wallet is different than the channel in the database")
			}
			channelToUse = &c
			break
		}
		if channelToUse == nil {
			return errors.Err("this wallet has channels but not a single one is ours! Expected claim_id: %s (%s)", s.DbChannelData.ChannelClaimID, s.DbChannelData.DesiredChannelName)
		}
	} else if s.DbChannelData.TransferState == shared.TransferStateComplete {
		return errors.Err("the channel was transferred but appears to have been abandoned!")
	} else if s.DbChannelData.ChannelClaimID != "" {
		return errors.Err("the database has a channel recorded (%s) but nothing was found in our control", s.DbChannelData.ChannelClaimID)
	}

	channelUsesOldMetadata := false
	if channelToUse != nil {
		channelUsesOldMetadata = channelToUse.Value.GetThumbnail() == nil
		if !channelUsesOldMetadata {
			return nil
		}
	}

	channelBidAmount := channelClaimAmount

	balanceResp, err := s.daemon.AccountBalance(nil)
	if err != nil {
		return err
	} else if balanceResp == nil {
		return errors.Err("no response")
	}
	balance, err := decimal.NewFromString(balanceResp.Available.String())
	if err != nil {
		return errors.Err(err)
	}

	if balance.LessThan(decimal.NewFromFloat(channelBidAmount)) {
		err = s.addCredits(channelBidAmount + 0.3)
		if err != nil {
			return err
		}
	}

	channelInfo, err := ytapi.ChannelInfo(s.DbChannelData.ChannelId)
	if err != nil {
		return err
	}

	thumbnail := channelInfo.Header.C4TabbedHeaderRenderer.Avatar.Thumbnails[len(channelInfo.Header.C4TabbedHeaderRenderer.Avatar.Thumbnails)-1].URL
	thumbnailURL, err := thumbs.MirrorThumbnail(thumbnail, s.DbChannelData.ChannelId, *s.Manager.AwsConfigs.GetS3AWSConfig())
	if err != nil {
		return err
	}

	var bannerURL *string
	if channelInfo.Header.C4TabbedHeaderRenderer.Banner.Thumbnails != nil {
		bURL, err := thumbs.MirrorThumbnail(channelInfo.Header.C4TabbedHeaderRenderer.Banner.Thumbnails[len(channelInfo.Header.C4TabbedHeaderRenderer.Banner.Thumbnails)-1].URL,
			"banner-"+s.DbChannelData.ChannelId,
			*s.Manager.AwsConfigs.GetS3AWSConfig(),
		)
		if err != nil {
			return err
		}
		bannerURL = &bURL
	}

	var languages []string = nil
	//we don't have this data without the API
	//if channelInfo.DefaultLanguage != "" {
	//	if channelInfo.DefaultLanguage == "iw" {
	//		channelInfo.DefaultLanguage = "he"
	//	}
	//	languages = []string{channelInfo.DefaultLanguage}
	//}
	var locations []jsonrpc.Location = nil
	if channelInfo.Topbar.DesktopTopbarRenderer.CountryCode != "" {
		locations = []jsonrpc.Location{{Country: util.PtrToString(channelInfo.Topbar.DesktopTopbarRenderer.CountryCode)}}
	}
	var c *jsonrpc.TransactionSummary
	claimCreateOptions := jsonrpc.ClaimCreateOptions{
		Title:        &channelInfo.Microformat.MicroformatDataRenderer.Title,
		Description:  &channelInfo.Microformat.MicroformatDataRenderer.Description,
		Tags:         tags_manager.GetTagsForChannel(s.DbChannelData.ChannelId),
		Languages:    languages,
		Locations:    locations,
		ThumbnailURL: &thumbnailURL,
	}
	if channelUsesOldMetadata {
		if s.DbChannelData.TransferState <= 1 {
			c, err = s.daemon.ChannelUpdate(s.DbChannelData.ChannelClaimID, jsonrpc.ChannelUpdateOptions{
				ClearTags:      util.PtrToBool(true),
				ClearLocations: util.PtrToBool(true),
				ClearLanguages: util.PtrToBool(true),
				ChannelCreateOptions: jsonrpc.ChannelCreateOptions{
					ClaimCreateOptions: claimCreateOptions,
					CoverURL:           bannerURL,
				},
			})
		} else {
			logUtils.SendInfoToSlack("%s (%s) has a channel with old metadata but isn't in our control anymore. Ignoring", s.DbChannelData.DesiredChannelName, s.DbChannelData.ChannelClaimID)
			return nil
		}
	} else {
		c, err = s.daemon.ChannelCreate(s.DbChannelData.DesiredChannelName, channelBidAmount, jsonrpc.ChannelCreateOptions{
			ClaimCreateOptions: claimCreateOptions,
			CoverURL:           bannerURL,
		})
	}

	if err != nil {
		return err
	}

	s.DbChannelData.ChannelClaimID = c.Outputs[0].ClaimID
	return s.Manager.ApiConfig.SetChannelClaimID(s.DbChannelData.ChannelId, s.DbChannelData.ChannelClaimID)
}

func (s *Sync) addCredits(amountToAdd float64) error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("addCredits").Add(time.Since(start))
	}(start)
	log.Printf("Adding %f credits", amountToAdd)
	lbrycrdd, err := logUtils.GetLbrycrdClient(s.Manager.LbrycrdDsn)
	if err != nil {
		return err
	}

	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	addressResp, err := s.daemon.AddressUnused(&defaultAccount)
	if err != nil {
		return err
	} else if addressResp == nil {
		return errors.Err("no response")
	}
	address := string(*addressResp)

	_, err = lbrycrdd.SimpleSend(address, amountToAdd)
	if err != nil {
		return err
	}

	wait := 15 * time.Second
	log.Println("Waiting " + wait.String() + " for lbryum to let us know we have the new transaction")
	time.Sleep(wait)

	return nil
}
