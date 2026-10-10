//go:build (darwin && arm64) || (linux && arm64)

package adapter

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const mappedFileQueueBytes = 16 * 1024
const mappedFileQueueName = "queue.dat"

// mappedFileMapping pins a pre-provisioned queue.dat descriptor and MAP_SHARED
// view. Its caller owns root and the transaction flock, and must join all byte
// access before Close. The backing file is process-crash state, not reboot
// durable; its owner must reinitialize it before peers start each boot.
type mappedFileMapping struct {
	mu        sync.Mutex
	root      *os.Root
	directory *os.File
	file      *os.File
	view      []byte
	closeErr  error
}

func openMappedFileMapping(root *os.Root) (*mappedFileMapping, error) {
	if root == nil {
		return nil, ErrMappedQueueInvalid
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, mappedFileError("open mapped queue directory", err)
	}
	closeDirectory := true
	defer func() {
		if closeDirectory {
			_ = directory.Close()
		}
	}()
	if err := mappedFileFilesystemAllowed(int(directory.Fd())); err != nil {
		return nil, err
	}
	before, err := root.Lstat(mappedFileQueueName)
	if err != nil {
		return nil, mappedFileError("inspect mapped queue path", err)
	}
	if !validMappedFileInfo(before) {
		return nil, ErrMappedQueueInvalid
	}
	file, err := root.OpenFile(mappedFileQueueName, os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, mappedFileError("open mapped queue file", err)
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()
	if err := mappedFileFilesystemAllowed(int(file.Fd())); err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, mappedFileError("inspect opened mapped queue", err)
	}
	current, err := root.Lstat(mappedFileQueueName)
	if err != nil {
		return nil, mappedFileError("recheck mapped queue path", err)
	}
	if !validMappedFileInfo(after) || !validMappedFileInfo(current) || !os.SameFile(before, after) || !os.SameFile(current, after) {
		return nil, ErrMappedQueueInvalid
	}
	view, err := unix.Mmap(int(file.Fd()), 0, mappedFileQueueBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, mappedFileError("map queue file", err)
	}
	closeDirectory = false
	closeFile = false
	return &mappedFileMapping{root: root, directory: directory, file: file, view: view}, nil
}

func validMappedFileInfo(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != mappedFileQueueBytes {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint32(stat.Uid) == uint32(os.Geteuid()) && uint32(stat.Mode)&07777 == 0600 && stat.Nlink == 1
}

func mappedFileError(operation string, err error) error {
	return errors.Join(ErrMappedQueueInvalid, fmt.Errorf("%s: %w", operation, err))
}

// Bytes returns the shared view while open. Callers must serialize all access
// against Close and follow the queue's transaction-lock contract.
func (m *mappedFileMapping) Bytes() []byte {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.view
}

// Validate rechecks the pinned descriptor and its root-relative name before a
// transaction. The caller must hold the sidecar flock across validation/use.
func (m *mappedFileMapping) Validate() error {
	if m == nil {
		return os.ErrClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil || m.directory == nil || m.root == nil {
		return os.ErrClosed
	}
	if err := mappedFileFilesystemAllowed(int(m.directory.Fd())); err != nil {
		return err
	}
	if err := mappedFileFilesystemAllowed(int(m.file.Fd())); err != nil {
		return err
	}
	opened, err := m.file.Stat()
	if err != nil {
		return mappedFileError("inspect mapped queue descriptor", err)
	}
	pathInfo, err := m.root.Lstat(mappedFileQueueName)
	if err != nil {
		return mappedFileError("inspect mapped queue path", err)
	}
	if !validMappedFileInfo(opened) || !validMappedFileInfo(pathInfo) || !os.SameFile(opened, pathInfo) {
		return ErrMappedQueueInvalid
	}
	return nil
}

// Close unmaps and closes the retained file and directory descriptors. It is
// safe to call repeatedly; the caller remains responsible for joining users.
func (m *mappedFileMapping) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.view == nil && m.file == nil && m.directory == nil {
		return m.closeErr
	}
	var errs []error
	if m.view != nil {
		if err := unix.Munmap(m.view); err != nil {
			errs = append(errs, fmt.Errorf("unmap queue file: %w", err))
		}
		m.view = nil
	}
	if m.file != nil {
		if err := m.file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close queue file: %w", err))
		}
		m.file = nil
	}
	if m.directory != nil {
		if err := m.directory.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close queue directory: %w", err))
		}
		m.directory = nil
	}
	m.closeErr = errors.Join(errs...)
	return m.closeErr
}
