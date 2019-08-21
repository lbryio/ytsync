package manager

import (
	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"
	"github.com/lbryio/ytsync/sdk"
)

func TransferChannelAndVideos(channel *Sync) error {
	cleanTransfer := true
	for _, video := range channel.syncedVideos {
		if !channel.syncedVideos[video.ClaimID].Published || channel.syncedVideos[video.ClaimID].Transferred || channel.syncedVideos[video.ClaimID].MetadataVersion != LatestMetadataVersion {
			continue
		}

		//Todo - Wait for prior sync to see that the publish is confirmed in lbrycrd?
		c, err := channel.daemon.ClaimSearch(nil, &video.ClaimID, nil, nil)
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
				ClaimCreateOptions: jsonrpc.ClaimCreateOptions{ClaimAddress: &channel.publishAddress},
			},
			Bid: util.PtrToString("0.009"), // Todo - Dont hardcode
		}

		videoStatus := sdk.VideoStatus{
			ChannelID:     channel.YoutubeChannelID,
			VideoID:       video.VideoID,
			ClaimID:       video.ClaimID,
			ClaimName:     video.ClaimName,
			Status:        VideoStatusPublished,
			IsTransferred: util.PtrToBool(true),
		}

		_, err = channel.daemon.StreamUpdate(video.ClaimID, streamUpdateOptions)
		if err != nil {
			cleanTransfer = false
			videoStatus.FailureReason = err.Error()
			videoStatus.Status = VideoStatusTranferFailed
			videoStatus.IsTransferred = util.PtrToBool(false)
		}
		statusErr := channel.APIConfig.MarkVideoStatus(videoStatus)
		if statusErr != nil {
			return errors.Err(err)
		}
		if err != nil {
			return errors.Err(err)
		}
	}
	// Todo - Transfer Channel as last step and post back to remote db that channel is transferred.
	//Transfer channel
	if !cleanTransfer {
		return errors.Err("A video has failed to transfer for the channel...skipping channel transfer")
	}
	channelClaim, err := channel.daemon.ClaimSearch(nil, &channel.lbryChannelID, nil, nil)
	if err != nil {
		return errors.Err(err)
	}
	if channelClaim == nil {
		return errors.Err("There is no channel claim for channel %s", channel.LbryChannelName)
	}
	updateOptions := jsonrpc.ChannelUpdateOptions{
		ChannelCreateOptions: jsonrpc.ChannelCreateOptions{
			ClaimCreateOptions: jsonrpc.ClaimCreateOptions{
				ClaimAddress: &channel.publishAddress,
			},
		},
	}
	_, err = channel.daemon.ChannelUpdate(channel.lbryChannelID, updateOptions)
	return errors.Err(err)
}
