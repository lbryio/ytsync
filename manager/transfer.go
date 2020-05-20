package manager

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/stop"
	"github.com/lbryio/lbry.go/v2/extras/util"
	"github.com/lbryio/ytsync/sdk"
	"github.com/lbryio/ytsync/timing"

	log "github.com/sirupsen/logrus"
)

func waitConfirmations(s *Sync) error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("waitConfirmations").Add(time.Since(start))
	}(start)
	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	allConfirmed := false
	waitCount := 0
waiting:
	for !allConfirmed && waitCount < 2 {
		utxolist, err := s.daemon.UTXOList(&defaultAccount, 1, 10000)
		if err != nil {
			return err
		} else if utxolist == nil {
			return errors.Err("no response")
		}

		for _, utxo := range utxolist.Items {
			if utxo.Confirmations <= 0 {
				err = s.waitForNewBlock()
				if err != nil {
					return err
				}
				waitCount++
				continue waiting
			}
		}
		allConfirmed = true
	}
	return nil
}

type abandonResponse struct {
	ClaimID string
	Error   error
	Amount  float64
}

func abandonSupports(s *Sync) (float64, error) {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("abandonSupports").Add(time.Since(start))
	}(start)
	totalPages := uint64(1)
	var allSupports []jsonrpc.Claim
	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return 0, err
	}
	for page := uint64(1); page <= totalPages; page++ {
		supports, err := s.daemon.SupportList(&defaultAccount, page, 50)
		if err != nil {
			return 0, errors.Prefix("cannot list claims", err)
		}
		allSupports = append(allSupports, (*supports).Items...)
		totalPages = (*supports).TotalPages
	}
	producerWG := &stop.Group{}

	claimIDChan := make(chan string, len(allSupports))
	abandonRspChan := make(chan abandonResponse, len(allSupports))
	alreadyAbandoned := make(map[string]bool, len(allSupports))
	producerWG.Add(1)
	go func() {
		defer producerWG.Done()
		for _, support := range allSupports {
			_, ok := alreadyAbandoned[support.ClaimID]
			if ok {
				continue
			}
			alreadyAbandoned[support.ClaimID] = true
			claimIDChan <- support.ClaimID
		}
	}()
	consumerWG := &stop.Group{}
	//TODO: remove this once the SDK team fixes their RPC bugs....
	s.daemon.SetRPCTimeout(60 * time.Second)
	defer s.daemon.SetRPCTimeout(5 * time.Minute)
	for i := 0; i < s.ConcurrentVideos; i++ {
		consumerWG.Add(1)
		go func() {
			defer consumerWG.Done()
		outer:
			for {
				claimID, more := <-claimIDChan
				if !more {
					return
				} else {
					summary, err := s.daemon.TxoSpend(util.PtrToString("support"), &claimID, nil, nil, nil, &defaultAccount)
					if err != nil {
						if strings.Contains(err.Error(), "Client.Timeout exceeded while awaiting headers") {
							log.Errorf("Support abandon for %s timed out, retrying...", claimID)
							summary, err = s.daemon.TxoSpend(util.PtrToString("support"), &claimID, nil, nil, nil, &defaultAccount)
							if err != nil {
								//TODO GUESS HOW MUCH LBC WAS RELEASED THAT WE DON'T KNOW ABOUT, because screw you SDK
								abandonRspChan <- abandonResponse{
									ClaimID: claimID,
									Error:   err,
									Amount:  0, // this is likely wrong, but oh well... there is literally nothing I can do about it
								}
								continue
							}
						} else {
							abandonRspChan <- abandonResponse{
								ClaimID: claimID,
								Error:   err,
								Amount:  0,
							}
							continue
						}
					}
					if summary == nil || len(*summary) < 1 {
						abandonRspChan <- abandonResponse{
							ClaimID: claimID,
							Error:   errors.Err("error abandoning supports: no outputs while abandoning %s", claimID),
							Amount:  0,
						}
						continue
					}
					var outputAmount float64
					for _, tx := range *summary {
						amount, err := strconv.ParseFloat(tx.Outputs[0].Amount, 64)
						if err != nil {
							abandonRspChan <- abandonResponse{
								ClaimID: claimID,
								Error:   errors.Err(err),
								Amount:  0,
							}
							continue outer
						}
						outputAmount += amount
					}
					if err != nil {
						abandonRspChan <- abandonResponse{
							ClaimID: claimID,
							Error:   errors.Err(err),
							Amount:  0,
						}
						continue
					}
					log.Infof("Abandoned supports of %.4f LBC for claim %s", outputAmount, claimID)
					abandonRspChan <- abandonResponse{
						ClaimID: claimID,
						Error:   nil,
						Amount:  outputAmount,
					}
					continue
				}
			}
		}()
	}
	producerWG.Wait()
	close(claimIDChan)
	consumerWG.Wait()
	close(abandonRspChan)

	totalAbandoned := 0.0
	for r := range abandonRspChan {
		if r.Error != nil {
			log.Errorf("Failed abandoning supports for %s: %s", r.ClaimID, r.Error.Error())
			continue
		}
		totalAbandoned += r.Amount
	}
	return totalAbandoned, nil
}

type updateInfo struct {
	ClaimID             string
	streamUpdateOptions *jsonrpc.StreamUpdateOptions
	videoStatus         *sdk.VideoStatus
}

func transferVideos(s *Sync) error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("transferVideos").Add(time.Since(start))
	}(start)
	cleanTransfer := true

	streamChan := make(chan updateInfo, s.ConcurrentVideos)
	account, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	streams, err := s.daemon.StreamList(&account, 1, 30000)
	if err != nil {
		return errors.Err(err)
	}
	producerWG := &stop.Group{}
	producerWG.Add(1)
	go func() {
		defer producerWG.Done()
		for _, video := range s.syncedVideos {
			if !video.Published || video.Transferred || video.MetadataVersion != LatestMetadataVersion {
				continue
			}

			var stream *jsonrpc.Claim = nil
			for _, c := range streams.Items {
				if c.ClaimID != video.ClaimID || (c.SigningChannel != nil && c.SigningChannel.ClaimID != s.lbryChannelID) {
					continue
				}
				stream = &c
				break
			}
			if stream == nil {
				return
			}

			streamUpdateOptions := jsonrpc.StreamUpdateOptions{
				StreamCreateOptions: &jsonrpc.StreamCreateOptions{
					ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
						ClaimAddress: &s.clientPublishAddress,
						FundingAccountIDs: []string{
							account,
						},
					},
				},
				Bid: util.PtrToString("0.005"), // Todo - Dont hardcode
			}
			videoStatus := sdk.VideoStatus{
				ChannelID:     s.YoutubeChannelID,
				VideoID:       video.VideoID,
				ClaimID:       video.ClaimID,
				ClaimName:     video.ClaimName,
				Status:        VideoStatusPublished,
				IsTransferred: util.PtrToBool(true),
			}
			streamChan <- updateInfo{
				ClaimID:             video.ClaimID,
				streamUpdateOptions: &streamUpdateOptions,
				videoStatus:         &videoStatus,
			}
		}
	}()

	consumerWG := &stop.Group{}
	for i := 0; i < s.ConcurrentVideos; i++ {
		consumerWG.Add(1)
		go func(worker int) {
			defer consumerWG.Done()
			for {
				ui, more := <-streamChan
				if !more {
					return
				} else {
					err := s.streamUpdate(&ui)
					if err != nil {
						cleanTransfer = false
					}
				}
			}
		}(i)
	}
	producerWG.Wait()
	close(streamChan)
	consumerWG.Wait()

	if !cleanTransfer {
		return errors.Err("A video has failed to transfer for the channel...skipping channel transfer")
	}
	return nil
}

func (s *Sync) streamUpdate(ui *updateInfo) error {
	start := time.Now()
	result, updateError := s.daemon.StreamUpdate(ui.ClaimID, *ui.streamUpdateOptions)
	timing.TimedComponent("transferStreamUpdate").Add(time.Since(start))
	if updateError != nil {
		ui.videoStatus.FailureReason = updateError.Error()
		ui.videoStatus.Status = VideoStatusTranferFailed
		ui.videoStatus.IsTransferred = util.PtrToBool(false)
	} else {
		ui.videoStatus.IsTransferred = util.PtrToBool(len(result.Outputs) != 0)
	}
	log.Infof("TRANSFERRED %t", *ui.videoStatus.IsTransferred)
	statusErr := s.APIConfig.MarkVideoStatus(*ui.videoStatus)
	if statusErr != nil {
		return errors.Prefix(statusErr.Error(), updateError)
	}
	return errors.Err(updateError)
}

func transferChannel(s *Sync) error {
	start := time.Now()
	defer func(start time.Time) {
		timing.TimedComponent("transferChannel").Add(time.Since(start))
	}(start)
	account, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	channelClaims, err := s.daemon.ChannelList(&account, 1, 50, nil)
	if err != nil {
		return errors.Err(err)
	}
	var channelClaim *jsonrpc.Transaction = nil
	for _, c := range channelClaims.Items {
		if c.ClaimID != s.lbryChannelID {
			continue
		}
		channelClaim = &c
		break
	}
	if channelClaim == nil {
		return nil
	}

	updateOptions := jsonrpc.ChannelUpdateOptions{
		Bid: util.PtrToString(fmt.Sprintf("%.6f", channelClaimAmount-0.005)),
		ChannelCreateOptions: jsonrpc.ChannelCreateOptions{
			ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
				ClaimAddress: &s.clientPublishAddress,
			},
		},
	}
	result, err := s.daemon.ChannelUpdate(s.lbryChannelID, updateOptions)
	if err != nil {
		return errors.Err(err)
	}
	log.Infof("TRANSFERRED %t", len(result.Outputs) != 0)

	return nil
}
