// Package nativeimgutil provides image utilities that do not depend on `qemu-img` binary.
package nativeimgutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	containerdfs "github.com/containerd/continuity/fs"
	"github.com/docker/go-units"
	"github.com/lima-vm/go-qcow2reader"
	"github.com/lima-vm/go-qcow2reader/convert"
	"github.com/lima-vm/go-qcow2reader/image/qcow2"
	"github.com/lima-vm/go-qcow2reader/image/raw"
	"github.com/lima-vm/lima/pkg/progressbar"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/sirupsen/logrus"
)

// CreateRawDataDisk creates an empty raw data disk.
func CreateRawDataDisk(dir string, size int) error {
	dataDisk := filepath.Join(dir, filenames.DataDisk)
	if _, err := os.Stat(dataDisk); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.Create(dataDisk)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(int64(size))
}

// ResizeRawDataDisk resizes a raw data disk.
func ResizeRawDataDisk(dir string, size int) error {
	dataDisk := filepath.Join(dir, filenames.DataDisk)
	return os.Truncate(dataDisk, int64(size))
}

// ConvertToRaw converts a source disk into a raw disk.
// source and dest may be same.
// ConvertToRaw is a NOP if source == dest, and no resizing is needed.
func ConvertToRaw(source, dest string, size *int64, allowSourceWithBackingFile bool) error {
	srcF, err := os.Open(source)
	if err != nil {
		return err
	}
	defer srcF.Close()
	srcImg, err := qcow2reader.Open(srcF)
	if err != nil {
		return fmt.Errorf("failed to detect the format of %q: %w", source, err)
	}
	if size != nil && *size < srcImg.Size() {
		return fmt.Errorf("specified size %d is smaller than the original image size (%d) of %q", *size, srcImg.Size(), source)
	}
	logrus.Infof("Converting %q (%s) to a raw disk %q", source, srcImg.Type(), dest)
	switch t := srcImg.Type(); t {
	case raw.Type:
		if err = srcF.Close(); err != nil {
			return err
		}
		return convertRawToRaw(source, dest, size)
	case qcow2.Type:
		if !allowSourceWithBackingFile {
			q, ok := srcImg.(*qcow2.Qcow2)
			if !ok {
				return fmt.Errorf("unexpected qcow2 image %T", srcImg)
			}
			if q.BackingFile != "" {
				return fmt.Errorf("qcow2 image %q has an unexpected backing file: %q", source, q.BackingFile)
			}
		}
	default:
		logrus.Warnf("image %q has an unexpected format: %q", source, t)
	}
	if err = srcImg.Readable(); err != nil {
		return fmt.Errorf("image %q is not readable: %w", source, err)
	}

	// Create a tmp file because source and dest can be same.
	destTmpF, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".lima-*.tmp")
	if err != nil {
		return err
	}
	destTmp := destTmpF.Name()
	defer os.RemoveAll(destTmp)
	defer destTmpF.Close()

	// Truncating before copy eliminates the seeks during copy and provide a
	// hint to the file system that may minimize allocations and fragmentation
	// of the file.
	if err := MakeSparse(destTmpF, srcImg.Size()); err != nil {
		return err
	}

	// Copy
	bar, err := progressbar.New(srcImg.Size())
	if err != nil {
		return err
	}
	bar.Start()
	err = convert.Convert(destTmpF, srcImg, convert.Options{Progress: bar})
	bar.Finish()
	if err != nil {
		return fmt.Errorf("failed to convert image: %w", err)
	}

	// Resize
	if size != nil {
		logrus.Infof("Expanding to %s", units.BytesSize(float64(*size)))
		if err = MakeSparse(destTmpF, *size); err != nil {
			return err
		}
	}
	if err = destTmpF.Close(); err != nil {
		return err
	}

	// Rename destTmp into dest
	if err = os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(destTmp, dest)
}

func convertRawToRaw(source, dest string, size *int64) error {
	if source != dest {
		// continuity attempts clonefile
		if err := containerdfs.CopyFile(dest, source); err != nil {
			return fmt.Errorf("failed to copy %q into %q: %w", source, dest, err)
		}
	}
	if size != nil {
		logrus.Infof("Expanding to %s", units.BytesSize(float64(*size)))
		destF, err := os.OpenFile(dest, os.O_RDWR, 0o644)
		if err != nil {
			return err
		}
		if err = MakeSparse(destF, *size); err != nil {
			_ = destF.Close()
			return err
		}
		return destF.Close()
	}
	return nil
}

func MakeSparse(f *os.File, n int64) error {
	if _, err := f.Seek(n, io.SeekStart); err != nil {
		return err
	}
	return f.Truncate(n)
}
