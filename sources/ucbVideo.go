package sources

import (
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sync"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/lbryio/lbry.go/errors"
	"github.com/lbryio/lbry.go/jsonrpc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
)

type ucbVideo struct {
	id              string
	title           string
	channel         string
	description     string
	publishedAt     time.Time
	dir             string
	claimNames      map[string]bool
	syncedVideosMux *sync.RWMutex
}

func NewUCBVideo(id, title, channel, description, publishedAt, dir string) *ucbVideo {
	p, _ := time.Parse(time.RFC3339Nano, publishedAt) // ignore parse errors
	return &ucbVideo{
		id:          id,
		title:       title,
		description: description,
		channel:     channel,
		dir:         dir,
		publishedAt: p,
	}
}

func (v *ucbVideo) ID() string {
	return v.id
}

func (v *ucbVideo) PlaylistPosition() int {
	return 0
}

func (v *ucbVideo) IDAndNum() string {
	return v.ID() + " (?)"
}

func (v *ucbVideo) PublishedAt() time.Time {
	return v.publishedAt
	//r := regexp.MustCompile(`(\d\d\d\d)-(\d\d)-(\d\d)`)
	//matches := r.FindStringSubmatch(v.title)
	//if len(matches) > 0 {
	//	year, _ := strconv.Atoi(matches[1])
	//	month, _ := strconv.Atoi(matches[2])
	//	day, _ := strconv.Atoi(matches[3])
	//	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	//}
	//return time.Now()
}

func (v *ucbVideo) getFilename() string {
	return v.dir + "/" + v.id + ".mp4"
}

func (v *ucbVideo) getClaimName(attempt int) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	suffix := ""
	if attempt > 1 {
		suffix = "-" + strconv.Itoa(attempt)
	}
	maxLen := 40 - len(suffix)

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

	return name + suffix
}

func (v *ucbVideo) getAbbrevDescription() string {
	maxLines := 10
	description := strings.TrimSpace(v.description)
	if strings.Count(description, "\n") < maxLines {
		return description
	}
	return strings.Join(strings.Split(description, "\n")[:maxLines], "\n") + "\n..."
}

func (v *ucbVideo) download() error {
	videoPath := v.getFilename()

	_, err := os.Stat(videoPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if err == nil {
		log.Debugln(v.id + " already exists at " + videoPath)
		return nil
	}

	creds := credentials.NewStaticCredentials("ID-GOES-HERE", "SECRET-GOES-HERE", "")
	s, err := session.NewSession(&aws.Config{Region: aws.String("us-east-2"), Credentials: creds})
	if err != nil {
		return err
	}
	downloader := s3manager.NewDownloader(s)

	out, err := os.Create(videoPath)
	if err != nil {
		return err
	}
	defer out.Close()

	log.Println("lbry-niko2/videos/" + v.channel + "/" + v.id)

	bytesWritten, err := downloader.Download(out, &s3.GetObjectInput{
		Bucket: aws.String("lbry-niko2"),
		Key:    aws.String("/videos/" + v.channel + "/" + v.id + ".mp4"),
	})
	if err != nil {
		return err
	} else if bytesWritten == 0 {
		return errors.Err("zero bytes written")
	}

	return nil
}

func (v *ucbVideo) saveThumbnail() error {
	resp, err := http.Get("https://s3.us-east-2.amazonaws.com/lbry-niko2/thumbnails/" + v.id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	creds := credentials.NewStaticCredentials("ID-GOES-HERE", "SECRET-GOES-HERE", "")
	s, err := session.NewSession(&aws.Config{Region: aws.String("us-east-2"), Credentials: creds})
	if err != nil {
		return err
	}
	uploader := s3manager.NewUploader(s)

	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket:      aws.String("berk.ninja"),
		Key:         aws.String("thumbnails/" + v.id),
		ContentType: aws.String("image/jpeg"),
		Body:        resp.Body,
	})

	return err
}

func (v *ucbVideo) publish(daemon *jsonrpc.Client, claimAddress string, amount float64, channelID string) (*SyncSummary, error) {
	options := jsonrpc.PublishOptions{
		Title:         &v.title,
		Author:        strPtr("UC Berkeley"),
		Description:   strPtr(v.getAbbrevDescription()),
		Language:      strPtr("en"),
		ClaimAddress:  &claimAddress,
		Thumbnail:     strPtr("https://berk.ninja/thumbnails/" + v.id),
		License:       strPtr("see description"),
		ChannelID:     &channelID,
		ChangeAddress: &claimAddress,
	}

	return publishAndRetryExistingNames(daemon, v.title, v.getFilename(), amount, options, v.claimNames, v.syncedVideosMux)
}

func (v *ucbVideo) Size() *int64 {
	return nil
}

func (v *ucbVideo) Sync(daemon *jsonrpc.Client, claimAddress string, amount float64, channelID string, maxVideoSize int, claimNames map[string]bool, syncedVideosMux *sync.RWMutex) (*SyncSummary, error) {
	v.claimNames = claimNames
	v.syncedVideosMux = syncedVideosMux
	//download and thumbnail can be done in parallel
	err := v.download()
	if err != nil {
		return nil, errors.Prefix("download error", err)
	}
	log.Debugln("Downloaded " + v.id)

	//err = v.SaveThumbnail()
	//if err != nil {
	//	return errors.WrapPrefix(err, "thumbnail error", 0)
	//}
	//log.Debugln("Created thumbnail for " + v.id)

	summary, err := v.publish(daemon, claimAddress, amount, channelID)
	if err != nil {
		return nil, errors.Prefix("publish error", err)
	}

	return summary, nil
}
