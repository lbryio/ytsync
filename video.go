package ytsync

import (
	"regexp"
	"strings"
	"time"
)

type video struct {
	id               string
	channelID        string
	channelTitle     string
	title            string
	description      string
	playlistPosition int64
	publishedAt      time.Time
	dir              string
}

func (v video) getFilename() string {
	return v.dir + "/" + v.id + ".mp4"
}

func (v video) getClaimName() string {
	maxLen := 40
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)

	chunks := strings.Split(strings.ToLower(strings.Trim(reg.ReplaceAllString(v.title, "-"), "-")), "-")

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

	return name
}

func (v video) getAbbrevDescription() string {
	maxLines := 10
	description := strings.TrimSpace(v.description)
	if strings.Count(description, "\n") < maxLines {
		return description
	}
	return strings.Join(strings.Split(description, "\n")[:maxLines], "\n") + "\n..."
}

// sorting videos
type byPublishedAt []video

func (a byPublishedAt) Len() int           { return len(a) }
func (a byPublishedAt) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byPublishedAt) Less(i, j int) bool { return a[i].publishedAt.Before(a[j].publishedAt) }

type byPlaylistPosition []video

func (a byPlaylistPosition) Len() int           { return len(a) }
func (a byPlaylistPosition) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byPlaylistPosition) Less(i, j int) bool { return a[i].playlistPosition < a[j].playlistPosition }
