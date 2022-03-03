package shared

import (
	"encoding/json"
	"time"

	"github.com/lbryio/lbry.go/v2/extras/errors"
)

type Fee struct {
	Amount   string `json:"amount"`
	Address  string `json:"address"`
	Currency string `json:"currency"`
}
type YoutubeChannel struct {
	ChannelId          string         `json:"channel_id"`
	TotalVideos        uint           `json:"total_videos"`
	TotalSubscribers   uint           `json:"total_subscribers"`
	DesiredChannelName string         `json:"desired_channel_name"`
	Fee                *Fee           `json:"fee"`
	ChannelClaimID     string         `json:"channel_claim_id"`
	TransferState      int            `json:"transfer_state"`
	PublishAddress     PublishAddress `json:"publish_address"`
	PublicKey          string         `json:"public_key"`
	LengthLimit        int            `json:"length_limit"`
	SizeLimit          int            `json:"size_limit"`
	LastUploadedVideo  string         `json:"last_uploaded_video"`
	WipeDB             bool           `json:"wipe_db"`
}

type PublishAddress struct {
	Address string `json:"address"`
	IsMine  bool   `json:"is_mine"`
}

func (p *PublishAddress) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.Err(err)
	}
	p.Address = s
	p.IsMine = false
	return nil
}

var FatalErrors = []string{
	":5279: read: connection reset by peer",
	"no space left on device",
	"NotEnoughFunds",
	"Cannot publish using channel",
	"cannot concatenate 'str' and 'NoneType' objects",
	"more than 90% of the space has been used.",
	"Couldn't find private key for id",
	"You already have a stream claim published under the name",
	"Missing inputs",
}
var ErrorsNoRetry = []string{
	"Requested format is not available",
	"non 200 status code received",
	"This video contains content from",
	"dont know which claim to update",
	"uploader has not made this video available in your country",
	"download error: AccessDenied: Access Denied",
	"Playback on other websites has been disabled by the video owner",
	"Error in daemon: Cannot publish empty file",
	"Error extracting sts from embedded url response",
	"Unable to extract signature tokens",
	"Client.Timeout exceeded while awaiting headers",
	"the video is too big to sync, skipping for now",
	"video is too long to process",
	"video is too short to process",
	"no compatible format available for this video",
	"Watch this video on YouTube.",
	"have blocked it on copyright grounds",
	"the video must be republished as we can't get the right size",
	"HTTP Error 403",
	"giving up after 0 fragment retries",
	"Sorry about that",
	"This video is not available",
	"requested format not available",
	"interrupted by user",
	"Sign in to confirm your age",
	"This video is unavailable",
	"video is a live stream and hasn't completed yet",
	"Premieres in",
	"Private video",
	"This live event will begin in",
	"This video has been removed by the uploader",
	"Premiere will begin shortly",
	"cannot unmarshal number 0.0",
	"default youtube thumbnail found",
	"livestream is likely bugged",
}
var WalletErrors = []string{
	"Not enough funds to cover this transaction",
	"failed: Not enough funds",
	"Error in daemon: Insufficient funds, please deposit additional LBC",
	//"Missing inputs",
}
var BlockchainErrors = []string{
	"txn-mempool-conflict",
	"too-long-mempool-chain",
}
var NeverRetryFailures = []string{
	"Error extracting sts from embedded url response",
	"Unable to extract signature tokens",
	"the video is too big to sync, skipping for now",
	"video is too long to process",
	"video is too short to process",
	"This video contains content from",
	"no compatible format available for this video",
	"Watch this video on YouTube.",
	"have blocked it on copyright grounds",
	"giving up after 0 fragment retries",
	"Sign in to confirm your age",
	"Playback on other websites has been disabled by the video owner",
	"uploader has not made this video available in your country",
	"This video has been removed by the uploader",
}

type SyncFlags struct {
	TakeOverExistingChannel bool
	SkipSpaceCheck          bool
	SyncUpdate              bool
	SingleRun               bool
	RemoveDBUnpublished     bool
	UpgradeMetadata         bool
	DisableTransfers        bool
	QuickSync               bool
	MaxTries                int
	Refill                  int
	Limit                   int
	Status                  string
	SecondaryStatus         string
	ChannelID               string
	SyncFrom                int64
	SyncUntil               int64
	ConcurrentJobs          int
	VideosLimit             int
	MaxVideoSize            int
	MaxVideoLength          time.Duration
}

// VideosToSync dynamically figures out how many videos should be synced for a given subs count if nothing was otherwise specified
func (f *SyncFlags) VideosToSync(totalSubscribers uint) int {
	if f.VideosLimit > 0 {
		return f.VideosLimit
	}
	defaultVideosToSync := map[int]int{
		10000: 1000,
		5000:  500,
		1000:  400,
		800:   250,
		600:   200,
		200:   80,
		100:   50,
		1:     10,
	}
	videosToSync := 0
	for s, r := range defaultVideosToSync {
		if int(totalSubscribers) >= s && r > videosToSync {
			videosToSync = r
		}
	}
	return videosToSync
}

func (f *SyncFlags) IsSingleChannelSync() bool {
	return f.ChannelID != ""
}

type VideoStatus struct {
	ChannelID       string
	VideoID         string
	Status          string
	ClaimID         string
	ClaimName       string
	FailureReason   string
	Size            *int64
	MetaDataVersion uint
	IsTransferred   *bool
}

const (
	StatusPending        = "pending"        // waiting for permission to sync
	StatusPendingEmail   = "pendingemail"   // permission granted but missing email
	StatusQueued         = "queued"         // in sync queue. will be synced soon
	StatusPendingUpgrade = "pendingupgrade" // in sync queue. will be synced soon
	StatusSyncing        = "syncing"        // syncing now
	StatusSynced         = "synced"         // done
	StatusWipeDb         = "pendingdbwipe"  // in sync queue. lbryum database will be pruned
	StatusFailed         = "failed"
	StatusFinalized      = "finalized"     // no more changes allowed
	StatusAbandoned      = "abandoned"     // deleted on youtube or banned
	StatusAgeRestricted  = "agerestricted" // one or more videos are age restricted and should be reprocessed with special keys
)

var SyncStatuses = []string{StatusPending, StatusPendingEmail, StatusPendingUpgrade, StatusQueued, StatusSyncing, StatusSynced, StatusFailed, StatusFinalized, StatusAbandoned, StatusWipeDb, StatusAgeRestricted}

const LatestMetadataVersion = 2

const (
	VideoStatusPublished      = "published"
	VideoStatusFailed         = "failed"
	VideoStatusUpgradeFailed  = "upgradefailed"
	VideoStatusUnpublished    = "unpublished"
	VideoStatusTransferFailed = "transferfailed"
)

var VideoSyncStatuses = []string{VideoStatusPublished, VideoStatusFailed, VideoStatusUpgradeFailed, VideoStatusUnpublished, VideoStatusTransferFailed}

const (
	TransferStateNotTouched = iota
	TransferStatePending
	TransferStateComplete
	TransferStateManual
)
