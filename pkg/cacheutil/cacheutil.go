package cacheutil

import (
	"context"
	"fmt"

	"github.com/lima-vm/lima/pkg/downloader"
	"github.com/lima-vm/lima/pkg/fileutils"
	"github.com/lima-vm/lima/pkg/limayaml"
)

// EnsureNerdctlArchiveCache prefetches the archive into the cache.
//
// EnsureNerdctlArchiveCache prefetches the nerdctl-full-VERSION-GOOS-GOARCH.tar.gz archive
// into the cache before launching the hostagent process, so that we can show the progress in tty.
// https://github.com/lima-vm/lima/issues/326
func EnsureNerdctlArchiveCache(ctx context.Context, y *limayaml.LimaYAML, created bool) (string, error) {
	if !*y.Containerd.System && !*y.Containerd.User {
		// nerdctl archive is not needed
		return "", nil
	}

	errs := make([]error, len(y.Containerd.Archives))
	for i, f := range y.Containerd.Archives {
		// Skip downloading again if the file is already in the cache
		if created && f.Arch == *y.Arch && !downloader.IsLocal(f.Location) {
			path, err := fileutils.CachedFile(f)
			if err == nil {
				return path, nil
			}
		}
		path, err := fileutils.DownloadFile(ctx, "", f, false, "the nerdctl archive", *y.Arch)
		if err != nil {
			errs[i] = err
			continue
		}
		if path == "" {
			if downloader.IsLocal(f.Location) {
				return f.Location, nil
			}
			return "", fmt.Errorf("cache did not contain %q", f.Location)
		}
		return path, nil
	}

	return "", fileutils.Errors(errs)
}
