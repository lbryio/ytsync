package namer

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var claimNameRegexp = regexp.MustCompile(`[=&#:$@%?;\\"/<>%{}|^~\x60[\]\s]`)

type Namer struct {
	mu    *sync.Mutex
	names map[string]bool
}

func NewNamer() *Namer {
	return &Namer{
		mu:    &sync.Mutex{},
		names: make(map[string]bool),
	}
}

func (n *Namer) SetNames(names map[string]bool) {
	n.names = names
}

func (n *Namer) GetNextName(prefix string) string {
	n.mu.Lock()
	defer n.mu.Unlock()

	attempt := 1
	var name string
	for {
		name = getClaimNameFromTitle(prefix, attempt)
		if _, exists := n.names[name]; !exists {
			break
		}
		attempt++
	}

	//if for some reasons the title can't be converted in a valid claim name (too short or not latin) then we use a hash
	attempt = 1
	if len(name) < 2 {
		sum := md5.Sum([]byte(prefix))
		for {
			name = fmt.Sprintf("%s-%d", hex.EncodeToString(sum[:])[:15], attempt)
			if _, exists := n.names[name]; !exists {
				break
			}
			attempt++
		}
	}

	n.names[name] = true

	return name
}

// TODO: clean this up some
func getClaimNameFromTitle(title string, attempt int) string {
	suffix := ""
	if attempt > 1 {
		suffix = "-" + strconv.Itoa(attempt)
	}
	maxLen := 40 - len(suffix)

	chunks := strings.Split(strings.ToLower(strings.Trim(claimNameRegexp.ReplaceAllString(title, "-"), "-")), "-")

	name := chunks[0]
	if len(name) > maxLen {
		return truncateUnicode(name, maxLen) + suffix
	}

	for _, chunk := range chunks[1:] {
		if chunk == "" {
			continue
		}
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

func truncateUnicode(name string, limit int) string {
	reNameBlacklist := regexp.MustCompile(`(&|>|<|\/|:|\n|\r)*`)
	name = reNameBlacklist.ReplaceAllString(name, "")
	result := name
	chars := 0
	for i := range name {
		if chars >= limit {
			result = name[:i]
			break
		}
		chars++
	}
	return result
}
