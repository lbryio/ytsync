package manager

import (
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/lbryio/lbry.go/v2/extras/errors"

	logUtils "github.com/lbryio/ytsync/v5/util"
)

func (s *Sync) getS3Downloader() (*s3manager.Downloader, error) {
	creds := credentials.NewStaticCredentials(s.AwsS3ID, s.AwsS3Secret, "")
	s3Session, err := session.NewSession(&aws.Config{Region: aws.String(s.AwsS3Region), Credentials: creds})
	if err != nil {
		return nil, errors.Prefix("error starting session: ", err)
	}
	downloader := s3manager.NewDownloader(s3Session)
	return downloader, nil
}
func (s *Sync) getS3Uploader() (*s3manager.Uploader, error) {
	creds := credentials.NewStaticCredentials(s.AwsS3ID, s.AwsS3Secret, "")
	s3Session, err := session.NewSession(&aws.Config{Region: aws.String(s.AwsS3Region), Credentials: creds})
	if err != nil {
		return nil, errors.Prefix("error starting session: ", err)
	}
	uploader := s3manager.NewUploader(s3Session)
	return uploader, nil
}

func (s *Sync) downloadWallet() error {
	defaultWalletDir, defaultTempWalletDir, key, err := s.getWalletPaths()
	if err != nil {
		return errors.Err(err)
	}
	downloader, err := s.getS3Downloader()
	if err != nil {
		return err
	}
	out, err := os.Create(defaultTempWalletDir)
	if err != nil {
		return errors.Prefix("error creating temp wallet: ", err)
	}
	defer out.Close()

	bytesWritten, err := downloader.Download(out, &s3.GetObjectInput{
		Bucket: aws.String(s.AwsS3Bucket),
		Key:    key,
	})
	if err != nil {
		// Casting to the awserr.Error type will allow you to inspect the error
		// code returned by the service in code. The error code can be used
		// to switch on context specific functionality. In this case a context
		// specific error message is printed to the user based on the bucket
		// and key existing.
		//
		// For information on other S3 API error codes see:
		// http://docs.aws.amazon.com/AmazonS3/latest/API/ErrorResponses.html
		if aerr, ok := err.(awserr.Error); ok {
			code := aerr.Code()
			if code == s3.ErrCodeNoSuchKey {
				return errors.Err("wallet not on S3")
			}
		}
		return err
	} else if bytesWritten == 0 {
		return errors.Err("zero bytes written")
	}

	err = os.Rename(defaultTempWalletDir, defaultWalletDir)
	if err != nil {
		return errors.Prefix("error replacing temp wallet for default wallet: ", err)
	}

	return nil
}

func (s *Sync) downloadBlockchainDB() error {
	defaultBDBDir, defaultTempBDBDir, key, err := s.getBlockchainDBPaths()
	if err != nil {
		return errors.Err(err)
	}
	files, err := filepath.Glob(defaultBDBDir + "*")
	if err != nil {
		return errors.Err(err)
	}
	for _, f := range files {
		err = os.Remove(f)
		if err != nil {
			return errors.Err(err)
		}
	}

	downloader, err := s.getS3Downloader()
	if err != nil {
		return errors.Err(err)
	}
	out, err := os.Create(defaultTempBDBDir)
	if err != nil {
		return errors.Prefix("error creating temp wallet: ", err)
	}
	defer out.Close()

	bytesWritten, err := downloader.Download(out, &s3.GetObjectInput{
		Bucket: aws.String(s.AwsS3Bucket),
		Key:    key,
	})
	if err != nil {
		// Casting to the awserr.Error type will allow you to inspect the error
		// code returned by the service in code. The error code can be used
		// to switch on context specific functionality. In this case a context
		// specific error message is printed to the user based on the bucket
		// and key existing.
		//
		// For information on other S3 API error codes see:
		// http://docs.aws.amazon.com/AmazonS3/latest/API/ErrorResponses.html
		if aerr, ok := err.(awserr.Error); ok {
			code := aerr.Code()
			if code == s3.ErrCodeNoSuchKey {
				return nil // let ytsync sync the database by itself
			}
		}
		return errors.Err(err)
	} else if bytesWritten == 0 {
		return errors.Err("zero bytes written")
	}

	err = os.Rename(defaultTempBDBDir, defaultBDBDir)
	if err != nil {
		return errors.Prefix("error replacing temp blockchain.db for default blockchain.db: ", err)
	}

	return nil
}

func (s *Sync) getWalletPaths() (defaultWallet, tempWallet string, key *string, err error) {
	defaultWallet = os.Getenv("HOME") + "/.lbryum/wallets/default_wallet"
	tempWallet = os.Getenv("HOME") + "/.lbryum/wallets/tmp_wallet"
	key = aws.String("/wallets/" + s.YoutubeChannelID)
	if logUtils.IsRegTest() {
		defaultWallet = os.Getenv("HOME") + "/.lbryum_regtest/wallets/default_wallet"
		tempWallet = os.Getenv("HOME") + "/.lbryum_regtest/wallets/tmp_wallet"
		key = aws.String("/regtest/" + s.YoutubeChannelID)
	}

	lbryumDir := os.Getenv("LBRYUM_DIR")
	if lbryumDir != "" {
		defaultWallet = lbryumDir + "/wallets/default_wallet"
		tempWallet = lbryumDir + "/wallets/tmp_wallet"
	}

	if _, err := os.Stat(defaultWallet); !os.IsNotExist(err) {
		return "", "", nil, errors.Err("default_wallet already exists")
	}
	return
}

func (s *Sync) getBlockchainDBPaths() (defaultDB, tempDB string, key *string, err error) {
	lbryumDir := os.Getenv("LBRYUM_DIR")
	if lbryumDir == "" {
		if logUtils.IsRegTest() {
			lbryumDir = os.Getenv("HOME") + "/.lbryum_regtest"
		} else {
			lbryumDir = os.Getenv("HOME") + "/.lbryum"
		}
	}
	defaultDB = lbryumDir + "/lbc_mainnet/blockchain.db"
	tempDB = lbryumDir + "/lbc_mainnet/tmp_blockchain.db"
	key = aws.String("/blockchain_dbs/" + s.YoutubeChannelID)
	if logUtils.IsRegTest() {
		defaultDB = lbryumDir + "/lbc_regtest/blockchain.db"
		tempDB = lbryumDir + "/lbc_regtest/tmp_blockchain.db"
		key = aws.String("/regtest_dbs/" + s.YoutubeChannelID)
	}
	return
}

func (s *Sync) uploadWallet() error {
	defaultWalletDir := logUtils.GetDefaultWalletPath()
	key := aws.String("/wallets/" + s.YoutubeChannelID)
	if logUtils.IsRegTest() {
		key = aws.String("/regtest/" + s.YoutubeChannelID)
	}

	if _, err := os.Stat(defaultWalletDir); os.IsNotExist(err) {
		return errors.Err("default_wallet does not exist")
	}

	uploader, err := s.getS3Uploader()
	if err != nil {
		return err
	}

	file, err := os.Open(defaultWalletDir)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s.AwsS3Bucket),
		Key:    key,
		Body:   file,
	})
	if err != nil {
		return err
	}

	return os.Remove(defaultWalletDir)
}

func (s *Sync) uploadBlockchainDB() error {
	defaultBDBDir, _, key, err := s.getBlockchainDBPaths()
	if err != nil {
		return errors.Err(err)
	}

	if _, err := os.Stat(defaultBDBDir); os.IsNotExist(err) {
		return errors.Err("blockchain.db does not exist")
	}

	uploader, err := s.getS3Uploader()
	if err != nil {
		return err
	}

	file, err := os.Open(defaultBDBDir)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s.AwsS3Bucket),
		Key:    key,
		Body:   file,
	})
	if err != nil {
		return err
	}

	return os.Remove(defaultBDBDir)
}
