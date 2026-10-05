//go:build darwin || linux

package main

import (
	"errors"
	"io"
	"os"
	"syscall"
)

func strictKeyFile(path string, private bool) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("key path is not a regular file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("key path is not a regular file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || uint32(os.Getuid()) != stat.Uid {
		return nil, errors.New("key file is not owned by the current user")
	}
	perm := info.Mode().Perm()
	if private && perm&0077 != 0 {
		return nil, errors.New("private key file is group/world accessible")
	}
	if !private && perm&0022 != 0 {
		return nil, errors.New("public key file is group/world writable")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 {
		return nil, errors.New("key file exceeds 8 KiB")
	}
	return data, nil
}
