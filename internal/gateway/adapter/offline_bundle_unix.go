//go:build darwin || linux

package adapter

import (
	"io"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/internal/gateway/bpv7"
	"golang.org/x/sys/unix"
)

// ReadOfflineBundle only reads a bounded regular file. It never executes,
// renames, deletes or acknowledges the source. Final symlinks and raced inode
// replacement are refused; input ownership is not sender authentication.
func ReadOfflineBundle(path string) (OfflineBundle, error) {
	data, err := readBundleFile(path, MaxOfflineBundleBytes, ErrOfflineBundle)
	if err != nil {
		return OfflineBundle{}, err
	}
	defer clear(data)
	return DecodeOfflineBundle(data)
}

func ReadBPv7(path string) (bpv7.Bundle, error) {
	data, err := readBundleFile(path, bpv7.MaxBytes, bpv7.ErrBundle)
	if err != nil {
		return bpv7.Bundle{}, err
	}
	defer clear(data)
	return bpv7.Decode(data)
}

func readBundleFile(path string, limit int64, invalid error) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, invalid
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, invalid
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "offline-bundle")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() > limit {
		return nil, invalid
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > int(limit) {
		clear(data)
		return nil, invalid
	}
	return data, nil
}
