package util

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lbryio/lbry.go/v2/extras/errors"
)

func CreateTarball(tarballFilePath string, filePaths []string) error {
	file, err := os.Create(tarballFilePath)
	if err != nil {
		return errors.Err("Could not create tarball file '%s', got error '%s'", tarballFilePath, err.Error())
	}
	defer file.Close()

	tarWriter := tar.NewWriter(file)
	defer tarWriter.Close()

	for _, filePath := range filePaths {
		err := addFileToTarWriter(filePath, tarWriter)
		if err != nil {
			return errors.Err("Could not add file '%s', to tarball, got error '%s'", filePath, err.Error())
		}
	}

	return nil
}

func addFileToTarWriter(filePath string, tarWriter *tar.Writer) error {
	file, err := os.Open(filePath)
	if err != nil {
		return errors.Err("Could not open file '%s', got error '%s'", filePath, err.Error())
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return errors.Err("Could not get stat for file '%s', got error '%s'", filePath, err.Error())
	}

	header := &tar.Header{
		Name:    stat.Name(),
		Size:    stat.Size(),
		Mode:    int64(stat.Mode()),
		ModTime: stat.ModTime(),
	}

	err = tarWriter.WriteHeader(header)
	if err != nil {
		return errors.Err("Could not write header for file '%s', got error '%s'", filePath, err.Error())
	}

	_, err = io.Copy(tarWriter, file)
	if err != nil {
		return errors.Err("Could not copy the file '%s' data to the tarball, got error '%s'", filePath, err.Error())
	}

	return nil
}

func Untar(tarball, target string) error {
	reader, err := os.Open(tarball)
	if err != nil {
		return errors.Err(err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return errors.Err(err)
		}

		path := filepath.Join(target, header.Name)
		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				return errors.Err(err)
			}
			continue
		}

		err = extractFile(path, info, tarReader)
		if err != nil {
			return err
		}
	}
	return nil
}

func extractFile(path string, info fs.FileInfo, tarReader *tar.Reader) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return errors.Err(err)
	}
	defer file.Close()
	_, err = io.Copy(file, tarReader)
	if err != nil {
		return errors.Err(err)
	}
	return nil
}
