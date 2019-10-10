package manager

import (
	"fmt"
	"github.com/lbryio/lbry.go/v2/extras/errors"
	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/util"
	"github.com/lbryio/ytsync/sdk"
	log "github.com/sirupsen/logrus"
	"strconv"
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
	alreadyAbandoned := make(map[string]bool, len(allSupports))
	totalAbandoned := 0.0
	for _, support := range allSupports {
		_, ok := alreadyAbandoned[support.ClaimID]
		if ok {
			continue
		}
		supportOnTransferredClaim := support.Address == s.clientPublishAddress //todo: probably not needed anymore
		if supportOnTransferredClaim {
			continue
		}
		alreadyAbandoned[support.ClaimID] = true
		summary, err := s.daemon.SupportAbandon(&support.ClaimID, nil, nil, nil, nil)
		if err != nil {
			return totalAbandoned, errors.Err(err)
		}
		if len(summary.Outputs) < 1 {
			return totalAbandoned, errors.Err("error abandoning supports: no outputs while abandoning %s", support.ClaimID)
		}
		outputAmount, err := strconv.ParseFloat(summary.Outputs[0].Amount, 64)
		if err != nil {
			return totalAbandoned, errors.Err(err)
		}
		totalAbandoned += outputAmount
		log.Infof("Abandoned supports of %.4f LBC for claim %s", outputAmount, support.ClaimID)
	}
	return totalAbandoned, nil
}

func transferVideos(s *Sync) error {
	cleanTransfer := true
	for _, video := range s.syncedVideos {
		if !video.Published || video.Transferred || video.MetadataVersion != LatestMetadataVersion {
			//log.Debugf("skipping video: %s", video.VideoID)
			continue
		}

		//Todo - Wait for prior sync to see that the publish is confirmed in lbrycrd?
		account, err := s.getDefaultAccount()
		if err != nil {
			return err
		}
		streams, err := s.daemon.StreamList(&account)
		if err != nil {
			return errors.Err(err)
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
			return nil
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

		result, updateError := s.daemon.StreamUpdate(video.ClaimID, streamUpdateOptions)
		if updateError != nil {
			cleanTransfer = false
			videoStatus.FailureReason = updateError.Error()
			videoStatus.Status = VideoStatusTranferFailed
			videoStatus.IsTransferred = util.PtrToBool(false)
		} else {
			videoStatus.IsTransferred = util.PtrToBool(len(result.Outputs) != 0)
		}
		log.Infof("TRANSFERRED %t", *videoStatus.IsTransferred)
		statusErr := s.APIConfig.MarkVideoStatus(videoStatus)
		if statusErr != nil {
			return errors.Prefix(statusErr.Error(), updateError)
		}
		if updateError != nil {
			return errors.Err(updateError)
		}
	}
	// Todo - Transfer Channel as last step and post back to remote db that channel is transferred.
	//Transfer channel
	if !cleanTransfer {
		return errors.Err("A video has failed to transfer for the channel...skipping channel transfer")
	}
	return nil
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
