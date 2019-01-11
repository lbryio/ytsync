package main

import (
	"time"

	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/lbrycrd"

	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

func (s *Sync) walletSetup() error {
	//prevent unnecessary concurrent execution
	s.walletMux.Lock()
	defer s.walletMux.Unlock()
	err := s.ensureChannelOwnership()
	if err != nil {
		return err
	}

	balanceResp, err := s.daemon.WalletBalance()
	if err != nil {
		return err
	} else if balanceResp == nil {
		return errors.Err("no response")
	}
	balance := decimal.Decimal(*balanceResp)
	log.Debugf("Starting balance is %s", balance.String())

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
	if numPublished > numOnSource && balance.LessThan(decimal.NewFromFloat(1)) {
		SendErrorToSlack("something is going on as we published more videos than those available on source: %d/%d", numPublished, numOnSource)
		minBalance = 1 //since we ended up in this function it means some juice is still needed
	}
	amountToAdd, _ := decimal.NewFromFloat(minBalance).Sub(balance).Float64()

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
		s.addCredits(amountToAdd)
	}

	claimAddress, err := s.daemon.WalletUnusedAddress()
	if err != nil {
		return err
	} else if claimAddress == nil {
		return errors.Err("could not get unused address")
	}
	s.claimAddress = string(*claimAddress)
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
	utxolist, err := s.daemon.UTXOList()
	if err != nil {
		return err
	} else if utxolist == nil {
		return errors.Err("no response")
	}

	target := 40
	slack := int(float32(0.1) * float32(target))
	count := 0

	for _, utxo := range *utxolist {
		if !utxo.IsClaim && !utxo.IsSupport && !utxo.IsUpdate && utxo.Amount.Cmp(decimal.New(0, 0)) == 1 {
			count++
		}
	}

	if count < target-slack {
		newAddresses := target - count

		balance, err := s.daemon.WalletBalance()
		if err != nil {
			return err
		} else if balance == nil {
			return errors.Err("no response")
		}

		log.Println("balance is " + decimal.Decimal(*balance).String())

		amountPerAddress := decimal.Decimal(*balance).Div(decimal.NewFromFloat(float64(target)))
		log.Infof("Putting %s credits into each of %d new addresses", amountPerAddress.String(), newAddresses)
		prefillTx, err := s.daemon.WalletPrefillAddresses(newAddresses, amountPerAddress, true)
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

	channels, err := s.daemon.ChannelList()
	if err != nil {
		return err
	} else if channels == nil {
		return errors.Err("no channel response")
	}
	//special case for wallets we don't retain full control anymore
	if len(*channels) > 1 {
		// This wallet is probably not under our control anymore but we still want to publish to it
		// here we shall check if within all the channels there is one that was created by ytsync
		SendInfoToSlack("we are dealing with a wallet that has multiple channels. This indicates that the wallet was probably transferred but we still want to sync their content. YoutubeID: %s", s.YoutubeChannelID)
		if s.lbryChannelID == "" {
			return errors.Err("this channel does not have a recorded claimID in the database. To prevent failures, updates are not supported until an entry is manually added in the database")
		}
		for _, c := range *channels {
			if c.ClaimID != s.lbryChannelID {
				if c.Name != s.LbryChannelName {
					return errors.Err("the channel in the wallet is different than the channel in the database")
				}
				return nil // we have the ytsync channel and both the claimID and the channelName from the database are correct
			}
		}
	}
	if len(*channels) == 1 {
		channel := (*channels)[0]
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

	balanceResp, err := s.daemon.WalletBalance()
	if err != nil {
		return err
	} else if balanceResp == nil {
		return errors.Err("no response")
	}
	balance := decimal.Decimal(*balanceResp)

	if balance.LessThan(decimal.NewFromFloat(channelBidAmount)) {
		err = s.addCredits(channelBidAmount + 0.1)
		if err != nil {
			return err
		}
	}

	c, err := s.daemon.ChannelNew(s.LbryChannelName, channelBidAmount)
	if err != nil {
		return err
	}
	s.lbryChannelID = c.ClaimID
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

	addressResp, err := s.daemon.WalletUnusedAddress()
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
