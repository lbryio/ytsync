package manager

import (
	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"
	"github.com/lbryio/ytsync/sdk"
)

func TransferChannel(channel *Sync) error {
	//Transfer channel
	for _, video := range channel.syncedVideos {
		//Todo - Wait for prior sync to see that the publish is confirmed in lbrycrd?
		c, err := channel.daemon.ClaimSearch(nil, &video.ClaimID, nil, nil)
		if err != nil {
			errors.Err(err)
		}
		if len(c.Claims) == 0 {
			errors.Err("cannot transfer: no claim found for this video")
		} else if len(c.Claims) > 1 {
			errors.Err("cannot transfer: too many claims. claimID: %s", video.ClaimID)
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

	return nil
}
