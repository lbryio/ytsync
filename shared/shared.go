package shared

import (
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
)

type Fee struct {
	Amount   string `json:"amount"`
	Address  string `json:"address"`
	Currency string `json:"currency"`
}
type YoutubeChannel struct {
	ChannelId          string `json:"channel_id"`
	TotalVideos        uint   `json:"total_videos"`
	TotalSubscribers   uint   `json:"total_subscribers"`
	DesiredChannelName string `json:"desired_channel_name"`
	Fee                *Fee   `json:"fee"`
	ChannelClaimID     string `json:"channel_claim_id"`
	TransferState      int    `json:"transfer_state"`
	PublishAddress     string `json:"publish_address"`
	PublicKey          string `json:"public_key"`
	LengthLimit        int    `json:"length_limit"`
	SizeLimit          int    `json:"size_limit"`
	LastUploadedVideo  string `json:"last_uploaded_video"`
	WipeDB             bool   `json:"wipe_db"`
}

var NeverRetryFailures = []string{
	"Error extracting sts from embedded url response",
	"Unable to extract signature tokens",
	"the video is too big to sync, skipping for now",
	"video is too long to process",
	"This video contains content from",
	"no compatible format available for this video",
	"Watch this video on YouTube.",
	"have blocked it on copyright grounds",
	"giving up after 0 fragment retries",
	"Sign in to confirm your age",
}

type SyncFlags struct {
	StopOnError             bool
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
	StatusFinalized      = "finalized" // no more changes allowed
	StatusAbandoned      = "abandoned" // deleted on youtube or banned
)

var SyncStatuses = []string{StatusPending, StatusPendingEmail, StatusPendingUpgrade, StatusQueued, StatusSyncing, StatusSynced, StatusFailed, StatusFinalized, StatusAbandoned}

const LatestMetadataVersion = 2

const (
	VideoStatusPublished     = "published"
	VideoStatusFailed        = "failed"
	VideoStatusUpgradeFailed = "upgradefailed"
	VideoStatusUnpublished   = "unpublished"
	VideoStatusTranferFailed = "transferfailed"
)

const (
	TransferStateNotTouched = iota
	TransferStatePending
	TransferStateComplete
	TransferStateManual
)

type AwsConfigs struct {
	AwsS3ID     string
	AwsS3Secret string
	AwsS3Region string
	AwsS3Bucket string
}

func (a *AwsConfigs) GetS3AWSConfig() *aws.Config {
	return &aws.Config{
		Credentials: credentials.NewStaticCredentials(a.AwsS3ID, a.AwsS3Secret, ""),
		Region:      &a.AwsS3Region,
	}
}
