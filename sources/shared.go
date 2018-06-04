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

var publishedNamesMutex sync.RWMutex
var publishedNames = map[string]bool{}

func publishAndRetryExistingNames(daemon *jsonrpc.Client, title, filename string, amount float64, options jsonrpc.PublishOptions) error {
	attempt := 0
	for {
		attempt++
		name := getClaimNameFromTitle(title, attempt)

		publishedNamesMutex.RLock()
		_, exists := publishedNames[name]
		publishedNamesMutex.RUnlock()
		if exists {
			log.Printf("name exists, retrying (%d attempts so far)\n", attempt)
			continue
		}
		//if for some reasons the title can't be converted in a valid claim name (too short or not latin) then we use a hash
		if len(name) < 2 {
			hasher := md5.New()
			hasher.Write([]byte(title))
			name = fmt.Sprintf("%s-%d", hex.EncodeToString(hasher.Sum(nil))[:15], attempt)
		}

		_, err := daemon.Publish(name, filename, amount, options)
		if err == nil || strings.Contains(err.Error(), "failed: Multiple claims (") {
			publishedNamesMutex.Lock()
			publishedNames[name] = true
			publishedNamesMutex.Unlock()
			if err == nil {
				return nil
			} else {
				log.Printf("name exists, retrying (%d attempts so far)\n", attempt)
				continue
			}
		} else {
			return err
		}
	}
}
