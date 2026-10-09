//go:build linux && arm64

package gateway_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func sharedMemoryFixturePath(name string) (string, error) {
	if !validSharedMemoryFixtureName(name) {
		return "", fmt.Errorf("invalid shared-memory fixture name %q", name)
	}
	return filepath.Join("/dev/shm", strings.TrimPrefix(name, "/")), nil
}

func provisionSharedMemoryObject(t *testing.T, name string) (func() error, error) {
	t.Helper()
	path, err := sharedMemoryFixturePath(name)
	if err != nil {
		return nil, err
	}
	object, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("exclusive /dev/shm create: %w", err)
	}
	partialFailure := func(cause error) (func() error, error) {
		closeErr := object.Close()
		removeErr := os.Remove(path)
		return nil, errors.Join(cause,
			wrapSharedMemoryCleanup("close partial /dev/shm object", closeErr),
			wrapSharedMemoryCleanup("remove partial /dev/shm object", removeErr))
	}
	if err := object.Chmod(0600); err != nil {
		return partialFailure(fmt.Errorf("chmod /dev/shm object: %w", err))
	}
	if err := object.Truncate(sharedMemoryFixtureBytes); err != nil {
		return partialFailure(fmt.Errorf("size /dev/shm object: %w", err))
	}
	image := make([]byte, sharedMemoryFixtureBytes)
	copy(image[4:8], "AGQ1")
	n, err := object.WriteAt(image, 0)
	if err != nil || n != len(image) {
		return partialFailure(fmt.Errorf("initialize /dev/shm object: wrote %d/%d bytes: %w", n, len(image), errors.Join(err, syscall.EIO)))
	}
	info, err := object.Stat()
	if err != nil {
		return partialFailure(fmt.Errorf("stat /dev/shm object: %w", err))
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != sharedMemoryFixtureBytes {
		return partialFailure(fmt.Errorf("verify /dev/shm object owner/mode/size: mode=%v size=%d", info.Mode(), info.Size()))
	}
	if err := object.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close /dev/shm object after initialization: %w", err), wrapSharedMemoryCleanup("remove /dev/shm object after close failure", os.Remove(path)))
	}
	return func() error { return os.Remove(path) }, nil
}

func wrapSharedMemoryCleanup(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
