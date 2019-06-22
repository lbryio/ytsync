package manager

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"
	"github.com/lbryio/lbry.go/lbrycrd"

	"github.com/lbryio/ytsync/tagsManager"
	"github.com/lbryio/ytsync/thumbs"

	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

func (s *Sync) enableAddressReuse() error {
	accountsResponse, err := s.daemon.AccountList()
	if err != nil {
		return errors.Err(err)
	}
	accounts := accountsResponse.LBCMainnet
	if os.Getenv("REGTEST") == "true" {
		accounts = accountsResponse.LBCRegtest
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
	balance, err := strconv.ParseFloat((string)(*balanceResp), 64)
	if err != nil {
		return errors.Err(err)
	}
	log.Debugf("Starting balance is %.4f", balance)

	n, err := s.CountVideos()
	if err != nil {
		return err
	}
	videosOnYoutube := int(n)

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

	if videosOnYoutube > s.Manager.videosLimit {
		videosOnYoutube = s.Manager.videosLimit
	}
	unallocatedVideos := videosOnYoutube - (publishedCount + failedCount)
	requiredBalance := float64(unallocatedVideos)*(publishAmount+estimatedMaxTxFee) + channelClaimAmount
	if s.Manager.upgradeMetadata {
		requiredBalance += float64(notUpgradedCount) * 0.001
	}

	refillAmount := 0.0
	if balance < requiredBalance || balance < minimumAccountBalance {
		refillAmount = math.Max(requiredBalance-balance, minimumRefillAmount)
	}

	if s.Refill > 0 {
		refillAmount += float64(s.Refill)
	}

	if refillAmount > 0 {
		err := s.addCredits(refillAmount)
		if err != nil {
			return errors.Err(err)
		}
	}

	claimAddress, err := s.daemon.AddressList(nil)
	if err != nil {
		return err
	} else if claimAddress == nil {
		return errors.Err("could not get unused address")
	}
	s.claimAddress = string((*claimAddress)[0]) //TODO: remove claimAddress completely
	if s.claimAddress == "" {
		return errors.Err("found blank claim address")
	}

	err = s.ensureEnoughUTXOs()
	if err != nil {
		return err
	}

	return nil
}

func (s *Sync) ensureEnoughUTXOs() error {
	accounts, err := s.daemon.AccountList()
	if err != nil {
		return errors.Err(err)
	}
	accountsNet := (*accounts).LBCMainnet
	if os.Getenv("REGTEST") == "true" {
		accountsNet = (*accounts).LBCRegtest
	}
	defaultAccount := ""
	for _, account := range accountsNet {
		if account.IsDefault {
			defaultAccount = account.ID
			break
		}
	}
	if defaultAccount == "" {
		return errors.Err("No default account found")
	}

	utxolist, err := s.daemon.UTXOList(&defaultAccount)
	if err != nil {
		return err
	} else if utxolist == nil {
		return errors.Err("no response")
	}

	target := 40
	slack := int(float32(0.1) * float32(target))
	count := 0

	for _, utxo := range *utxolist {
		amount, _ := strconv.ParseFloat(utxo.Amount, 64)
		if !utxo.IsClaim && !utxo.IsSupport && !utxo.IsUpdate && amount > 0.001 {
			count++
		}
	}
	log.Infof("utxo count: %d", count)
	if count < target-slack {
		balance, err := s.daemon.AccountBalance(&defaultAccount)
		if err != nil {
			return err
		} else if balance == nil {
			return errors.Err("no response")
		}

		balanceAmount, err := strconv.ParseFloat((string)(*balance), 64)
		if err != nil {
			return errors.Err(err)
		}
		maxUTXOs := uint64(500)
		desiredUTXOCount := uint64(math.Floor((balanceAmount) / 0.1))
		if desiredUTXOCount > maxUTXOs {
			desiredUTXOCount = maxUTXOs
		}
		log.Infof("Splitting balance of %s evenly between %d UTXOs", *balance, desiredUTXOCount)

		broadcastFee := 0.1
		prefillTx, err := s.daemon.AccountFund(defaultAccount, defaultAccount, fmt.Sprintf("%.4f", balanceAmount-broadcastFee), desiredUTXOCount, false)
		if err != nil {
			return err
		} else if prefillTx == nil {
			return errors.Err("no response")
		}

		err = s.waitForNewBlock()
		if err != nil {
			return err
		}
	} else if !allUTXOsConfirmed(utxolist) {
		log.Println("Waiting for previous txns to confirm")
		err := s.waitForNewBlock()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Sync) waitForNewBlock() error {
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
		time.Sleep(10 * time.Second)
		status, err = s.daemon.Status()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Sync) ensureChannelOwnership() error {
	if s.LbryChannelName == "" {
		return errors.Err("no channel name set")
	}
	//@TODO: get rid of this when imported channels are supported
	if s.YoutubeChannelID == "UCW-thz5HxE-goYq8yPds1Gw" {
		return nil
	}
	channels, err := s.daemon.ChannelList(nil, 1, 50)
	if err != nil {
		return err
	} else if channels == nil {
		return errors.Err("no channel response")
	}
	//special case for wallets we don't retain full control anymore
	if len((*channels).Items) > 1 {
		// This wallet is probably not under our control anymore but we still want to publish to it
		// here we shall check if within all the channels there is one that was created by ytsync
		SendInfoToSlack("we are dealing with a wallet that has multiple channels. This indicates that the wallet was probably transferred but we still want to sync their content. YoutubeID: %s", s.YoutubeChannelID)
		if s.lbryChannelID == "" {
			return errors.Err("this channel does not have a recorded claimID in the database. To prevent failures, updates are not supported until an entry is manually added in the database")
		}
		for _, c := range (*channels).Items {
			if c.ClaimID != s.lbryChannelID {
				if c.Name != s.LbryChannelName {
					return errors.Err("the channel in the wallet is different than the channel in the database")
				}
				return nil // we have the ytsync channel and both the claimID and the channelName from the database are correct
			}
		}
	}
	channelUsesOldMetadata := false
	if len((*channels).Items) == 1 {
		channel := ((*channels).Items)[0]
		if channel.Name == s.LbryChannelName {
			channelUsesOldMetadata = channel.Value.GetThumbnail() == nil
			//TODO: eventually get rid of this when the whole db is filled
			if s.lbryChannelID == "" {
				err = s.Manager.apiConfig.SetChannelClaimID(s.YoutubeChannelID, channel.ClaimID)
			} else if channel.ClaimID != s.lbryChannelID {
				return errors.Err("the channel in the wallet is different than the channel in the database")
			}
			s.lbryChannelID = channel.ClaimID
			if !channelUsesOldMetadata {
				return err
			}
		} else {
			return errors.Err("this channel does not belong to this wallet! Expected: %s, found: %s", s.LbryChannelName, channel.Name)
		}
	}

	channelBidAmount := channelClaimAmount

	balanceResp, err := s.daemon.AccountBalance(nil)
	if err != nil {
		return err
	} else if balanceResp == nil {
		return errors.Err("no response")
	}
	balance, err := decimal.NewFromString((string)(*balanceResp))
	if err != nil {
		return errors.Err(err)
	}

	if balance.LessThan(decimal.NewFromFloat(channelBidAmount)) {
		err = s.addCredits(channelBidAmount + 0.1)
		if err != nil {
			return err
		}
	}
	client := &http.Client{
		Transport: &transport.APIKey{Key: s.APIConfig.YoutubeAPIKey},
	}

	service, err := youtube.New(client)
	if err != nil {
		return errors.Prefix("error creating YouTube service", err)
	}

	response, err := service.Channels.List("snippet,brandingSettings").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return errors.Prefix("error getting channel details", err)
	}

	if len(response.Items) < 1 {
		return errors.Err("youtube channel not found")
	}

	channelInfo := response.Items[0].Snippet
	channelBranding := response.Items[0].BrandingSettings

	thumbnail := thumbs.GetBestThumbnail(channelInfo.Thumbnails)
	thumbnailURL, err := thumbs.MirrorThumbnail(thumbnail.Url, s.YoutubeChannelID, s.Manager.GetS3AWSConfig())
	if err != nil {
		return err
	}

	var bannerURL *string
	if channelBranding.Image != nil && channelBranding.Image.BannerImageUrl != "" {
		bURL, err := thumbs.MirrorThumbnail(channelBranding.Image.BannerImageUrl, "banner-"+s.YoutubeChannelID, s.Manager.GetS3AWSConfig())
		if err != nil {
			return err
		}
		bannerURL = &bURL
	}

	var languages []string = nil
	if channelInfo.DefaultLanguage != "" {
		languages = []string{channelInfo.DefaultLanguage}
	}
	var locations []jsonrpc.Location = nil
	if channelInfo.Country != "" {
		locations = []jsonrpc.Location{{Country: util.PtrToString(channelInfo.Country)}}
	}
	var c *jsonrpc.TransactionSummary
	claimCreateOptions := jsonrpc.ClaimCreateOptions{
		Title:        &channelInfo.Title,
		Description:  &channelInfo.Description,
		Tags:         tagsManager.GetTagsForChannel(s.YoutubeChannelID),
		Languages:    languages,
		Locations:    locations,
		ThumbnailURL: &thumbnailURL,
	}
	if channelUsesOldMetadata {
		c, err = s.daemon.ChannelUpdate(s.lbryChannelID, jsonrpc.ChannelUpdateOptions{
			ClearTags:      util.PtrToBool(true),
			ClearLocations: util.PtrToBool(true),
			ClearLanguages: util.PtrToBool(true),
			ChannelCreateOptions: jsonrpc.ChannelCreateOptions{
				ClaimCreateOptions: claimCreateOptions,
				CoverURL:           bannerURL,
			},
		})
	} else {
		c, err = s.daemon.ChannelCreate(s.LbryChannelName, channelBidAmount, jsonrpc.ChannelCreateOptions{
			ClaimCreateOptions: claimCreateOptions,
			CoverURL:           bannerURL,
		})
	}

	if err != nil {
		return err
	}
	s.lbryChannelID = c.Outputs[0].ClaimID
	return s.Manager.apiConfig.SetChannelClaimID(s.YoutubeChannelID, s.lbryChannelID)
}

func allUTXOsConfirmed(utxolist *jsonrpc.UTXOListResponse) bool {
	if utxolist == nil {
		return false
	}

	if len(*utxolist) < 1 {
		return false
	}

	for _, utxo := range *utxolist {
		if utxo.Height == 0 {
			return false
		}
	}

	return true
}

func (s *Sync) addCredits(amountToAdd float64) error {
	log.Printf("Adding %f credits", amountToAdd)
	var lbrycrdd *lbrycrd.Client
	var err error
	if s.LbrycrdString == "" {
		lbrycrdd, err = lbrycrd.NewWithDefaultURL()
		if err != nil {
			return err
		}
	} else {
		lbrycrdd, err = lbrycrd.New(s.LbrycrdString)
		if err != nil {
			return err
		}
	}

	addressResp, err := s.daemon.AddressUnused(nil)
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
