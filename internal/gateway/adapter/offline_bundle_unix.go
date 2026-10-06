//go:build darwin || linux

package adapter

import (
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ReadOfflineBundle only reads a bounded regular file. It never executes,
// renames, deletes or acknowledges the source. Final symlinks and raced inode
// replacement are refused; input ownership is not sender authentication.
func ReadOfflineBundle(path string) (OfflineBundle, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return OfflineBundle{}, ErrOfflineBundle
	}
	before, err := os.Lstat(path)
	if err != nil {
		return OfflineBundle{}, err
	}
	if !before.Mode().IsRegular() || before.Size() > MaxOfflineBundleBytes {
		return OfflineBundle{}, ErrOfflineBundle
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return OfflineBundle{}, err
	}
	f := os.NewFile(uintptr(fd), "offline-bundle")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return OfflineBundle{}, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() > MaxOfflineBundleBytes {
		return OfflineBundle{}, ErrOfflineBundle
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxOfflineBundleBytes+1))
	if err != nil {
		return OfflineBundle{}, err
	}
	defer clear(data)
	return DecodeOfflineBundle(data)
}
