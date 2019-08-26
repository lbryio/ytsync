package manager

import (
	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"
	"github.com/lbryio/ytsync/sdk"
	log "github.com/sirupsen/logrus"
)

func waitConfirmations(s *Sync) error {
	allConfirmed := false
waiting:
	for !allConfirmed {
		utxolist, err := s.daemon.UTXOList(nil)
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

func abandonSupports(s *Sync) error {
	totalPages := uint64(1)
	var allSupports []jsonrpc.Claim
	for page := uint64(1); page <= totalPages; page++ {
		supports, err := s.daemon.SupportList(nil, page, 50)
		if err != nil {
			return errors.Prefix("cannot list claims", err)
		}
		allSupports = append(allSupports, (*supports).Items...)
		totalPages = (*supports).TotalPages
	}
	return nil
}

func transferVideos(s *Sync) error {
	err := waitConfirmations(s)
	if err != nil {
		return err
	}
	cleanTransfer := true
	for _, video := range s.syncedVideos {
		if !video.Published || video.Transferred || video.MetadataVersion != LatestMetadataVersion {
			log.Debugf("skipping video: %s", video.VideoID)
			continue
		}

		//Todo - Wait for prior sync to see that the publish is confirmed in lbrycrd?
		c, err := s.daemon.ClaimSearch(nil, &video.ClaimID, nil, nil)
		if err != nil {
			return errors.Err(err)
		}
		if len(c.Claims) == 0 {
			return errors.Err("cannot transfer: no claim found for this video")
		} else if len(c.Claims) > 1 {
			return errors.Err("cannot transfer: too many claims. claimID: %s", video.ClaimID)
		}

		streamUpdateOptions := jsonrpc.StreamUpdateOptions{
			StreamCreateOptions: &jsonrpc.StreamCreateOptions{
				ClaimCreateOptions: jsonrpc.ClaimCreateOptions{ClaimAddress: &s.publishAddress},
			},
			Bid: util.PtrToString("0.009"), // Todo - Dont hardcode
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
		}
		log.Printf("TRANSFER RESULT %+v", *result) //TODO: actually check the results to be sure it worked
		statusErr := s.APIConfig.MarkVideoStatus(videoStatus)
		if statusErr != nil {
			return errors.Err(statusErr)
		}
		if updateError != nil {
			return errors.Err(err)
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
	channelClaim, err := s.daemon.ClaimSearch(nil, &s.lbryChannelID, nil, nil)
	if err != nil {
		return errors.Err(err)
	}
	if channelClaim == nil || len(channelClaim.Claims) == 0 {
		return errors.Err("There is no channel claim for channel %s", s.LbryChannelName)
	}
	updateOptions := jsonrpc.ChannelUpdateOptions{
		ChannelCreateOptions: jsonrpc.ChannelCreateOptions{
			ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
				ClaimAddress: &s.publishAddress,
			},
		},
	}
	result, err := s.daemon.ChannelUpdate(s.lbryChannelID, updateOptions)
	log.Printf("TRANSFER RESULT %+v", *result) //TODO: actually check the results to be sure it worked

	return errors.Err(err)
}
