package manager

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uber-go/atomic"

	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/stop"
	"github.com/lbryio/lbry.go/v2/extras/util"

	"github.com/lbryio/ytsync/sdk"

	log "github.com/sirupsen/logrus"
)

func waitConfirmations(s *Sync) error {
	defaultAccount, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	allConfirmed := false
waiting:
	for !allConfirmed {
		utxolist, err := s.daemon.UTXOList(&defaultAccount)
		if err != nil {
			return err
		} else if utxolist == nil {
			return errors.Err("no response")
		}

		for _, utxo := range *utxolist {
			if utxo.Confirmations <= 0 {
				err = s.waitForNewBlock()
				if err != nil {
					return err
				}
				continue waiting
			}
		}
		allConfirmed = true
	}
	return nil
}

func abandonSupports(s *Sync) (float64, error) {
	type abandonResponse struct {
		ClaimID string
		Error   error
		Amount  float64
	}

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

	claimIDChan := make(chan string)
	abandonRspChan := make(chan abandonResponse)
	collectorChan := make(chan bool)

	alreadyAbandoned := make(map[string]bool, len(allSupports))
	totalAbandoned := atomic.NewFloat64(0)

	go func() {
		for r := range abandonRspChan {
			if r.Error != nil {
				log.Errorf("Failed abandoning supports for %s: %s", r.ClaimID, r.Error.Error())
				continue
			}
			totalAbandoned.Add(r.Amount)
		}
		close(collectorChan)
	}()

	consumerWG := &stop.Group{}
	//TODO: remove this once the SDK team fixes their RPC bugs....
	s.daemon.SetRPCTimeout(30 * time.Second)
	defer s.daemon.SetRPCTimeout(40 * time.Minute)
	for i := 0; i < s.ConcurrentVideos; i++ {
		consumerWG.Add(1)
		go func() {
			defer consumerWG.Done()
			for {
				claimID, more := <-claimIDChan
				if !more {
					return
				}

				summary, err := s.daemon.SupportAbandon(&claimID, nil, nil, nil, nil)
				if err != nil {
					if strings.Contains(err.Error(), "Client.Timeout exceeded while awaiting headers") {
						log.Errorf("Support abandon for %s timed out, retrying...", claimID)
						summary, err = s.daemon.SupportAbandon(&claimID, nil, nil, nil, nil)
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
						abandonRspChan <- abandonResponse{ClaimID: claimID, Error: err}
						continue
					}
				}

				if len(summary.Outputs) < 1 {
					abandonRspChan <- abandonResponse{
						ClaimID: claimID,
						Error:   errors.Err("abandoning supports: no outputs for %s", claimID),
					}
					continue
				}

				outputAmount, err := strconv.ParseFloat(summary.Outputs[0].Amount, 64)
				if err != nil {
					abandonRspChan <- abandonResponse{ClaimID: claimID, Error: errors.Err(err)}
					continue
				}

				log.Infof("Abandoned supports of %.4f LBC for claim %s", outputAmount, claimID)
				abandonRspChan <- abandonResponse{ClaimID: claimID, Amount: outputAmount}
				continue
			}
		}()
	}

	for _, support := range allSupports {
		_, ok := alreadyAbandoned[support.ClaimID]
		if ok {
			continue
		}
		alreadyAbandoned[support.ClaimID] = true
		claimIDChan <- support.ClaimID
	}
	close(claimIDChan)

	consumerWG.Wait()
	close(abandonRspChan)
	<-collectorChan

	return totalAbandoned.Load(), nil
}

type updateInfo struct {
	ClaimID             string
	streamUpdateOptions *jsonrpc.StreamUpdateOptions
	videoStatus         *sdk.VideoStatus
}

func transferVideos(s *Sync) error {
	cleanTransfer := true

	streamChan := make(chan updateInfo, s.ConcurrentVideos)
	account, err := s.getDefaultAccount()
	if err != nil {
		return err
	}
	streams, err := s.daemon.StreamList(&account)
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
			for _, c := range *streams {
				if c.ClaimID != video.ClaimID {
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
					ClaimCreateOptions: jsonrpc.ClaimCreateOptions{ClaimAddress: &s.clientPublishAddress},
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
	result, updateError := s.daemon.StreamUpdate(ui.ClaimID, *ui.streamUpdateOptions)
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
