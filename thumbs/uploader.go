package thumbs

import (
	"io"
	"net/http"
	"os"

	"github.com/lbryio/lbry.go/v2/extras/errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/youtube/v3"
)

type thumbnailUploader struct {
	name        string
	originalUrl string
	mirroredUrl string
	s3Config    aws.Config
}

const thumbnailPath = "/tmp/ytsync_thumbnails/"
const ThumbnailEndpoint = "https://thumbnails.lbry.com/"

func (u *thumbnailUploader) downloadThumbnail() error {
	_ = os.Mkdir(thumbnailPath, 0777)
	img, err := os.Create("/tmp/ytsync_thumbnails/" + u.name)
	if err != nil {
		return errors.Err(err)
	}
	defer img.Close()

	resp, err := http.Get(u.originalUrl)
	if err != nil {
		return errors.Err(err)
	}
	defer resp.Body.Close()

	_, err = io.Copy(img, resp.Body)
	if err != nil {
		return errors.Err(err)
	}
	return nil
}

func (u *thumbnailUploader) uploadThumbnail() error {
	key := &u.name
	thumb, err := os.Open("/tmp/ytsync_thumbnails/" + u.name)
	if err != nil {
		return errors.Err(err)
	}
	defer thumb.Close()

	s3Session, err := session.NewSession(&u.s3Config)
	if err != nil {
		return errors.Err(err)
	}

	uploader := s3manager.NewUploader(s3Session)

	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket:       aws.String("thumbnails.lbry.com"),
		Key:          key,
		Body:         thumb,
		ACL:          aws.String("public-read"),
		ContentType:  aws.String("image/jpeg"),
		CacheControl: aws.String("public, max-age=2592000"),
	})
	u.mirroredUrl = ThumbnailEndpoint + u.name
	return errors.Err(err)
}

func (u *thumbnailUploader) deleteTmpFile() {
	err := os.Remove("/tmp/ytsync_thumbnails/" + u.name)
	if err != nil {
		log.Infof("failed to delete local thumbnail file: %s", err.Error())
	}
}
func MirrorThumbnail(url string, name string, s3Config aws.Config) (string, error) {
	tu := thumbnailUploader{
		originalUrl: url,
		name:        name,
		s3Config:    s3Config,
	}
	err := tu.downloadThumbnail()
	if err != nil {
		return "", err
	}
	defer tu.deleteTmpFile()

	err = tu.uploadThumbnail()
	if err != nil {
		return "", err
	}

	return tu.mirroredUrl, nil
}

func GetBestThumbnail(thumbnails *youtube.ThumbnailDetails) *youtube.Thumbnail {
	if thumbnails.Maxres != nil {
		return thumbnails.Maxres
	} else if thumbnails.High != nil {
		return thumbnails.High
	} else if thumbnails.Medium != nil {
		return thumbnails.Medium
	} else if thumbnails.Standard != nil {
		return thumbnails.Standard
	}
	return thumbnails.Default
}
