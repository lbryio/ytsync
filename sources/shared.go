package sources

import (
	"strings"

	"github.com/lbryio/lbry.go/jsonrpc"
	"github.com/lbryio/lbry.go/ytsync/namer"
)

type SyncSummary struct {
	ClaimID   string
	ClaimName string
}

func publishAndRetryExistingNames(daemon *jsonrpc.Client, title, filename string, amount float64, options jsonrpc.PublishOptions, namer *namer.Namer) (*SyncSummary, error) {
	for {
		name := namer.GetNextName(title)
		response, err := daemon.Publish(name, filename, amount, options)
		if err != nil {
			if strings.Contains(err.Error(), "failed: Multiple claims (") {
				continue
			}
			return nil, err
		}
		return &SyncSummary{ClaimID: response.ClaimID, ClaimName: name}, nil
	}
}
