package sources

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"crypto/md5"
	"encoding/hex"

	"github.com/lbryio/lbry.go/jsonrpc"
	log "github.com/sirupsen/logrus"
)

var titleRegexp = regexp.MustCompile(`[^a-zA-Z0-9]+`)

type SyncSummary struct {
	ClaimID   string
	ClaimName string
}

func getClaimNameFromTitle(title string, attempt int) string {
	suffix := ""
	if attempt > 1 {
		suffix = "-" + strconv.Itoa(attempt)
	}
	maxLen := 40 - len(suffix)

	chunks := strings.Split(strings.ToLower(strings.Trim(titleRegexp.ReplaceAllString(title, "-"), "-")), "-")

	name := chunks[0]
	if len(name) > maxLen {
		return name[:maxLen]
	}

	for _, chunk := range chunks[1:] {
		tmpName := name + "-" + chunk
		if len(tmpName) > maxLen {
			if len(name) < 20 {
				name = tmpName[:maxLen]
			}
			break
		}
		name = tmpName
	}

	return name + suffix
}

func publishAndRetryExistingNames(daemon *jsonrpc.Client, title, filename string, amount float64, options jsonrpc.PublishOptions, claimNames map[string]bool, syncedVideosMux *sync.RWMutex) (*SyncSummary, error) {
	attempt := 0
	for {
		attempt++
		name := getClaimNameFromTitle(title, attempt)

		syncedVideosMux.Lock()
		_, exists := claimNames[name]
		if exists {
			log.Printf("name exists, retrying (%d attempts so far)", attempt)
			syncedVideosMux.Unlock()
			continue
		}
		claimNames[name] = false
		syncedVideosMux.Unlock()

		//if for some reasons the title can't be converted in a valid claim name (too short or not latin) then we use a hash
		if len(name) < 2 {
			hasher := md5.New()
			hasher.Write([]byte(title))
			name = fmt.Sprintf("%s-%d", hex.EncodeToString(hasher.Sum(nil))[:15], attempt)
		}

		response, err := daemon.Publish(name, filename, amount, options)
		if err == nil || strings.Contains(err.Error(), "failed: Multiple claims (") {
			syncedVideosMux.Lock()
			claimNames[name] = true
			syncedVideosMux.Unlock()
			if err == nil {
				return &SyncSummary{ClaimID: response.ClaimID, ClaimName: name}, nil
			} else {
				log.Printf("name exists, retrying (%d attempts so far)", attempt)
				continue
			}
		} else {
			return nil, err
		}
	}
}
