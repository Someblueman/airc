//go:build darwin || linux

package adapter

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// openPrivateDirectory validates and exclusively locks an owner-controlled root.
func openPrivateDirectory(path string) (*os.Root, *os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, errors.New("private connector requires a clean absolute directory")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !unixTrustedAncestor(info) || current == path && (!unixPathOwner(info) || info.Mode().Perm() != 0700) {
			return nil, nil, errors.New("private connector directory is not owner-controlled")
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	lock, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, nil, err
	}
	info, err := lock.Stat()
	if err != nil || !os.SameFile(before, info) {
		_ = lock.Close()
		_ = root.Close()
		return nil, nil, errors.New("private connector directory changed during open")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		_ = root.Close()
		return nil, nil, err
	}
	return root, lock, nil
}
