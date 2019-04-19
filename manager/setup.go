package manager

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"
	"github.com/lbryio/lbry.go/lbrycrd"
	"github.com/lbryio/ytsync/thumbs"

	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

func (s *Sync) walletSetup() error {
	//prevent unnecessary concurrent execution
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

	var numOnSource int
	if s.LbryChannelName == "@UCBerkeley" {
		numOnSource = 10104
	} else {
		n, err := s.CountVideos()
		if err != nil {
			return err
		}
		numOnSource = int(n)
	}

	log.Debugf("Source channel has %d videos", numOnSource)
	if numOnSource == 0 {
		return nil
	}

	s.syncedVideosMux.RLock()
	numPublished := len(s.syncedVideos) //should we only count published videos? Credits are allocated even for failed ones...
	s.syncedVideosMux.RUnlock()
	log.Debugf("We already allocated credits for %d videos", numPublished)

	if numOnSource-numPublished > s.Manager.videosLimit {
		numOnSource = s.Manager.videosLimit
	}

	minBalance := (float64(numOnSource)-float64(numPublished))*(publishAmount+0.1) + channelClaimAmount
	if numPublished > numOnSource && balance < 1 {
		SendErrorToSlack("something is going on as we published more videos than those available on source: %d/%d", numPublished, numOnSource)
		minBalance = 1 //since we ended up in this function it means some juice is still needed
	}
	amountToAdd := minBalance - balance

	if s.Refill > 0 {
		if amountToAdd < 0 {
			amountToAdd = float64(s.Refill)
		} else {
			amountToAdd += float64(s.Refill)
		}
	}

	if amountToAdd > 0 {
		if amountToAdd < 1 {
			amountToAdd = 1 // no reason to bother adding less than 1 credit
		}
		err := s.addCredits(amountToAdd)
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
	s.claimAddress = string((*claimAddress)[0])
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
		if account.IsDefaultAccount {
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
		if !utxo.IsClaim && !utxo.IsSupport && !utxo.IsUpdate && amount != 0.0 {
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
		broadcastFee := 0.01
		amountToSplit := fmt.Sprintf("%.6f", balanceAmount-broadcastFee)

		log.Infof("Splitting balance of %s evenly between 40 UTXOs", *balance)

		prefillTx, err := s.daemon.AccountFund(defaultAccount, defaultAccount, amountToSplit, uint64(target))
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
	if s.YoutubeChannelID == "UCkK9UDm_ZNrq_rIXCz3xCGA" || s.YoutubeChannelID == "UCW-thz5HxE-goYq8yPds1Gw" {
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
	if len((*channels).Items) == 1 {
		channel := ((*channels).Items)[0]
		if channel.Name == s.LbryChannelName {
			//TODO: eventually get rid of this when the whole db is filled
			if s.lbryChannelID == "" {
				err = s.Manager.apiConfig.SetChannelClaimID(s.YoutubeChannelID, channel.ClaimID)
			} else if channel.ClaimID != s.lbryChannelID {
				return errors.Err("the channel in the wallet is different than the channel in the database")
			}
			s.lbryChannelID = channel.ClaimID
			return err
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

	response, err := service.Channels.List("snippet").Id(s.YoutubeChannelID).Do()
	if err != nil {
		return errors.Prefix("error getting channel details", err)
	}

	if len(response.Items) < 1 {
		return errors.Err("youtube channel not found")
	}

	channelInfo := response.Items[0].Snippet

	thumbnail := channelInfo.Thumbnails.Default
	if channelInfo.Thumbnails.Maxres != nil {
		thumbnail = channelInfo.Thumbnails.Maxres
	} else if channelInfo.Thumbnails.High != nil {
		thumbnail = channelInfo.Thumbnails.High
	} else if channelInfo.Thumbnails.Medium != nil {
		thumbnail = channelInfo.Thumbnails.Medium
	} else if channelInfo.Thumbnails.Standard != nil {
		thumbnail = channelInfo.Thumbnails.Standard
	}
	thumbnailURL, err := thumbs.MirrorThumbnail(thumbnail.Url, s.YoutubeChannelID, aws.Config{
		Credentials: credentials.NewStaticCredentials(s.AwsS3ID, s.AwsS3Secret, ""),
		Region:      &s.AwsS3Region,
	})
	if err != nil {
		return err
	}
	var languages []string = nil
	if channelInfo.DefaultLanguage != "" {
		languages = []string{channelInfo.DefaultLanguage}
	}
	var locations []jsonrpc.Location = nil
	if channelInfo.Country != "" {
		locations = []jsonrpc.Location{{Country: util.PtrToString(channelInfo.Country)}}
	}
	c, err := s.daemon.ChannelCreate(s.LbryChannelName, channelBidAmount, jsonrpc.ChannelCreateOptions{
		ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
			Title:        channelInfo.Title,
			Description:  channelInfo.Description,
			Tags:         nil,
			Languages:    languages,
			Locations:    locations,
			ThumbnailURL: &thumbnailURL,
		},
	})
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
