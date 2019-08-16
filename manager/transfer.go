package manager

import (
	"github.com/lbryio/lbry.go/extras/errors"
	"github.com/lbryio/lbry.go/extras/jsonrpc"
	"github.com/lbryio/lbry.go/extras/util"
)

func TransferChannel(channel *Sync) error {
	//Transfer channel
	for _, video := range channel.syncedVideos {
		//Todo - Wait for prior sync to see that the publish is confirmed in lbrycrd?
		//Todo - We need to fix the ClaimSearch call in lbry.go for 38.5 lbrynet
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

		_, err = channel.daemon.StreamUpdate(video.ClaimID, streamUpdateOptions)
		if err != nil {
			return err
		}
		// Todo - Post to remote db that video is transferred
	}

	// Todo - Transfer Channel as last step and post back to remote db that channel is transferred.

	return nil
}
