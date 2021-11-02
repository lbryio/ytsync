package local

import (
	"errors"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/lbry.go/v2/extras/util"
)

type LocalSDKPublisher struct {
	channelID string
	publishBid float64
	lbrynet *jsonrpc.Client
}

func NewLocalSDKPublisher(sdkAddr, channelID string, publishBid float64) (*LocalSDKPublisher, error) {
	lbrynet := jsonrpc.NewClient(sdkAddr)
	lbrynet.SetRPCTimeout(5 * time.Minute)

	status, err := lbrynet.Status()
	if err != nil {
		return nil, err
	}

	if !status.IsRunning {
		return nil, errors.New("SDK is not running")
	}

	// Should check to see if the SDK owns the channel

	// Should check to see if wallet is unlocked
	// but jsonrpc.Client doesn't have WalletStatus method
	// so skip for now

	// Should check to see if streams are configured to be reflected and warn if not
	// but jsonrpc.Client doesn't have SettingsGet method to see if streams are reflected
	// so use File.UploadingToReflector as a proxy for now

	publisher := LocalSDKPublisher {
		channelID: channelID,
		publishBid: publishBid,
		lbrynet: lbrynet,
	}
	return &publisher, nil
}

func (p *LocalSDKPublisher) Publish(video PublishableVideo) (chan error, error) {
	streamCreateOptions := jsonrpc.StreamCreateOptions {
		ClaimCreateOptions: jsonrpc.ClaimCreateOptions {
			Title:        &video.Title,
			Description:  &video.Description,
			Languages:    video.Languages,
			ThumbnailURL: &video.ThumbnailURL,
			Tags:         video.Tags,
		},
		ReleaseTime: &video.ReleaseTime,
		ChannelID:   &p.channelID,
		License:     util.PtrToString("Copyrighted (contact publisher)"),
	}

	txSummary, err := p.lbrynet.StreamCreate(video.ClaimName, video.FullLocalPath, p.publishBid, streamCreateOptions)
	if err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() {
		for {
			fileListResponse, fileIndex, err := findFileByTxid(p.lbrynet, txSummary.Txid)
			if err != nil {
				log.Errorf("Error finding file by txid: %v", err)
				done <- err
				return
			}
			if fileListResponse == nil {
				log.Errorf("Could not find file in list with correct txid")
				done <- err
				return
			}

			fileStatus := fileListResponse.Items[fileIndex]
			if fileStatus.IsFullyReflected {
				log.Info("Stream is fully reflected")
				break
			}
			if !fileStatus.UploadingToReflector {
				log.Warn("Stream is not being uploaded to a reflector. Check your lbrynet settings if this is a mistake.")
				break
			}
			log.Infof("Stream reflector progress: %d%%", fileStatus.ReflectorProgress)
			time.Sleep(5 * time.Second)
		}
		done <- nil
	}()

	return done, nil
}

// if jsonrpc.Client.FileList is extended to match the actual jsonrpc schema, this can be removed
func findFileByTxid(client *jsonrpc.Client, txid string) (*jsonrpc.FileListResponse, int, error) {
	response, err := client.FileList(0, 20)
	for {
		if err != nil {
			log.Errorf("Error getting file list page: %v", err)
			return nil, 0, err
		}
		index := sort.Search(len(response.Items), func (i int) bool { return response.Items[i].Txid == txid })
		if index < len(response.Items) {
			return response, index, nil
		}
		if response.Page >= response.TotalPages {
			return nil, 0, nil
		}
		response, err = client.FileList(response.Page + 1, 20)
	}
}
