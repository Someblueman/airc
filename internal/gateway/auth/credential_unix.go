//go:build unix

package auth

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func privateDirectory(path string) (int, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, "", ErrCredential
	}
	fd, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, "", ErrCredential
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Uid != uint32(os.Geteuid()) || info.Mode&0077 != 0 {
		_ = unix.Close(fd)
		return -1, "", ErrCredential
	}
	return fd, filepath.Base(path), nil
}

func openPrivate(path string) (*os.File, error) {
	dir, name, err := privateDirectory(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrCredential
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Uid != uint32(os.Geteuid()) || info.Mode&0077 != 0 {
		_ = unix.Close(fd)
		return nil, ErrCredential
	}
	return os.NewFile(uintptr(fd), path), nil
}

func createPrivate(path string, data []byte) error {
	dir, name, err := privateDirectory(path)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrCredential
	}
	f := os.NewFile(uintptr(fd), path)
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		_ = unix.Unlinkat(dir, name, 0)
		return ErrCredential
	}
	if err := finishCreation(f, data); err != nil {
		_ = unix.Unlinkat(dir, name, 0)
		return ErrCredential
	}
	// Failure here is reported without overwriting/removing the synced file.
	if err := unix.Fsync(dir); err != nil {
		return ErrCredential
	}
	return nil
}
