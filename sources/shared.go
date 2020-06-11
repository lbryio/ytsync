package sources

import (
	"strings"
	"sync"

	"github.com/lbryio/lbry.go/v2/extras/jsonrpc"
	"github.com/lbryio/ytsync/v5/namer"
)

type SyncSummary struct {
	ClaimID   string
	ClaimName string
}

func publishAndRetryExistingNames(daemon *jsonrpc.Client, title, filename string, amount float64, options jsonrpc.StreamCreateOptions, namer *namer.Namer, walletLock *sync.RWMutex) (*SyncSummary, error) {
	walletLock.RLock()
	defer walletLock.RUnlock()
	for {
		name := namer.GetNextName(title)
		response, err := daemon.StreamCreate(name, filename, amount, options)
		if err != nil {
			if strings.Contains(err.Error(), "failed: Multiple claims (") {
				continue
			}
			return nil, err
		}
		PublishedClaim := response.Outputs[0]
		return &SyncSummary{ClaimID: PublishedClaim.ClaimID, ClaimName: name}, nil
	}
}
