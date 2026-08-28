package disk

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Usage struct {
	Path        string `json:"path"`
	TotalBytes  uint64 `json:"total_bytes"`
	FreeBytes   uint64 `json:"free_bytes"`
	SeedlyBytes uint64 `json:"seedly_bytes"`
	OtherBytes  uint64 `json:"other_bytes"`
}

// UsageForReports disk space for the filesystem that holds downloadsPath.
// Seedly usage is the sum of downloadsPath plus metaPath/dbPath when they
// live on the same filesystem.
func UsageFor(downloadsPath, metaPath, dbPath string) (Usage, error) {
	u := Usage{Path: downloadsPath}

	var fs syscall.Statfs_t
	if err := syscall.Statfs(downloadsPath, &fs); err != nil {
		return u, fmt.Errorf("statfs %s: %w", downloadsPath, err)
	}
	bsize := uint64(fs.Bsize)
	if bsize == 0 {
		bsize = 512
	}
	u.TotalBytes = fs.Blocks * bsize
	u.FreeBytes = fs.Bavail * bsize

	var seedly uint64
	for _, p := range []string{downloadsPath, metaPath, filepath.Dir(dbPath)} {
		if p == "" {
			continue
		}
		if !sameFilesystem(downloadsPath, p) {
			continue
		}
		n, err := dirSize(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return u, err
		}
		seedly += n
	}
	u.SeedlyBytes = seedly

	used := u.TotalBytes - u.FreeBytes
	if used > seedly {
		u.OtherBytes = used - seedly
	} else {
		u.OtherBytes = 0
		// Seedly measurement can briefly exceed "used" due to sparse files / rounding.
		if seedly > used {
			u.SeedlyBytes = used
		}
	}
	return u, nil
}

func sameFilesystem(a, b string) bool {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

func dirSize(root string) (uint64, error) {
	var total uint64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total, err
}
