package ytsync

import (
	"strings"
	"time"

	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/jsonrpc"
	"github.com/lbryio/lbry.go/lbrycrd"

	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

func (s *Sync) walletSetup() error {
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

	var numOnSource uint64
	if s.LbryChannelName == "@UCBerkeley" {
		numOnSource = 10104
	} else {
		numOnSource, err = s.CountVideos()
		if err != nil {
			return err
		}
	}
	log.Debugf("Source channel has %d videos", numOnSource)

	numPublished, err := s.daemon.NumClaimsInChannel(s.LbryChannelName)
	if err != nil {
		return err
	}
	log.Debugf("We already published %d videos", numPublished)

	minBalance := (float64(numOnSource)-float64(numPublished))*publishAmount + channelClaimAmount
	amountToAdd, _ := decimal.NewFromFloat(minBalance).Sub(balance).Float64()
	amountToAdd *= 1.5 // add 50% margin for fees, future publishes, etc

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

	if !allUTXOsConfirmed(utxolist) {
		log.Println("Waiting for previous txns to confirm") // happens if you restarted the daemon soon after a previous publish run
		s.waitUntilUTXOsConfirmed()
	}

	target := 30
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
			return errors.Err("no response")
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
	if s.LbryChannelName == "" {
		return errors.Err("no channel name set")
	}

	channels, err := s.daemon.ChannelList()
	if err != nil {
		return err
	} else if channels == nil {
		return errors.Err("no channel response")
	}

	isChannelMine := false
	for _, channel := range *channels {
		if channel.Name == s.LbryChannelName {
			isChannelMine = true
		} else {
			return errors.Err("this wallet has multiple channels. maybe something went wrong during setup?")
		}
	}
	if isChannelMine {
		return nil
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
			return errors.Err("Channel exists and we don't own it. Pick another channel.")
		}
		log.Println("Channel exists and we don't own it. Outbidding existing claim.")
		channelBidAmount, _ = channel.Certificate.Amount.Add(decimal.NewFromFloat(channelClaimAmount)).Float64()
	}

	balanceResp, err := s.daemon.WalletBalance()
	if err != nil {
		return err
	} else if balanceResp == nil {
		return errors.Err("no response")
	}
	balance := decimal.Decimal(*balanceResp)

	if balance.LessThan(decimal.NewFromFloat(channelBidAmount)) {
		s.addCredits(channelBidAmount + 0.1)
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
	lbrycrdd, err := lbrycrd.NewWithDefaultURL()
	if err != nil {
		return err
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

	log.Println("Waiting for transaction to be confirmed")
	return s.waitUntilUTXOsConfirmed()
}
