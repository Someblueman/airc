//go:build darwin || linux

package oscore

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func ownedExecutable(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", store.ErrInvalid
	}
	leaf, err := os.Lstat(path)
	if err != nil || !leaf.Mode().IsRegular() {
		return "", errors.Join(store.ErrInvalid, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	for current := resolved; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0) {
			return "", store.ErrInvalid
		}
		if current == resolved {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
				return "", store.ErrInvalid
			}
		} else if !info.IsDir() || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return "", store.ErrInvalid
		}
		if current == filepath.Dir(current) {
			return resolved, nil
		}
	}
}
