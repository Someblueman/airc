//go:build darwin || linux

package adapter

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type appendLogInfo struct {
	Device, Inode uint64
	Size          int64
}

func appendLogFileInfo(f *os.File) (appendLogInfo, error) {
	info, err := f.Stat()
	if err != nil {
		return appendLogInfo{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !unixPathOwner(info) || !ok || stat.Nlink != 1 || info.Size() > MaxAppendLogBytes {
		return appendLogInfo{}, errors.New("append log requires a bounded private single-link regular file")
	}
	return appendLogInfo{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Size: info.Size()}, nil
}

func (r *AppendLogReader) open(generation uint64) (*os.File, appendLogInfo, error) {
	f, err := r.files.root.OpenFile("logs/"+appendLogName(generation), os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, appendLogInfo{}, err
	}
	info, err := appendLogFileInfo(f)
	if err != nil {
		_ = f.Close()
		return nil, appendLogInfo{}, err
	}
	return f, info, nil
}
