package thumbs

import (
	"io"
	"net/http"
	"os"

	"github.com/lbryio/errors.go"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/prometheus/common/log"
)

type thumbnailUploader struct {
	name        string
	originalUrl string
	mirroredUrl string
	s3Config    aws.Config
}

const thumbnailPath = "/tmp/ytsync_thumbnails/"

func (u *thumbnailUploader) downloadThumbnail() error {
	_ = os.Mkdir(thumbnailPath, 0750)
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
	key := aws.String("/thumbnails/" + u.name)
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
		Bucket: aws.String("thumbnails.lbry.com"),
		Key:    key,
		Body:   thumb,
	})
	u.mirroredUrl = "https://thumbnails.lbry.com/" + u.name
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
