package iso9660util

import (
	"io"
	"os"
	"path"

	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	"github.com/sirupsen/logrus"
)

type Entry struct {
	Path   string
	Reader io.Reader
}

func Write(isoPath, label string, layout []Entry) error {
	if err := os.RemoveAll(isoPath); err != nil {
		return err
	}

	isoFile, err := os.Create(isoPath)
	if err != nil {
		return err
	}

	defer isoFile.Close()

	workdir, err := os.MkdirTemp("", "diskfs_iso")
	if err != nil {
		return err
	}
	logrus.Debugf("Creating iso file %s", isoFile.Name())
	logrus.Debugf("Using %s as workspace", workdir)
	fs, err := iso9660.Create(isoFile, 0, 0, 0, workdir)
	if err != nil {
		return err
	}

	for _, f := range layout {
		if _, err := WriteFile(fs, f.Path, f.Reader); err != nil {
			return err
		}
	}

	finalizeOptions := iso9660.FinalizeOptions{
		RockRidge:        true,
		VolumeIdentifier: label,
	}
	if err := fs.Finalize(finalizeOptions); err != nil {
		return err
	}

	return isoFile.Close()
}

func WriteFile(fs filesystem.FileSystem, pathStr string, r io.Reader) (int64, error) {
	if dir := path.Dir(pathStr); dir != "" && dir != "/" {
		if err := fs.Mkdir(dir); err != nil {
			return 0, err
		}
	}
	f, err := fs.OpenFile(pathStr, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

func IsISO9660(imagePath string) (bool, error) {
	imageFile, err := os.Open(imagePath)
	if err != nil {
		return false, err
	}
	defer imageFile.Close()

	fileInfo, err := imageFile.Stat()
	if err != nil {
		return false, err
	}
	_, err = iso9660.Read(imageFile, fileInfo.Size(), 0, 0)
	return err == nil, nil
}
