//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrMailboxResultPending = errors.New("file mailbox has an unread result")

// FileMailbox owns one private directory. Producers atomically rename a complete
// request to request.json; processing.json survives interruption until a durable
// result is published. An unread result prevents claiming another request.
// Use sequentially. This boundary does not authenticate content or claim journal admission.
type FileMailbox struct {
	root *os.Root
	lock *os.File
}

func OpenFileMailbox(path string) (*FileMailbox, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("file mailbox requires a clean absolute directory")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !unixTrustedAncestor(info) || current == path && (!unixPathOwner(info) || info.Mode().Perm() != 0700) {
			return nil, errors.New("file mailbox directory is not owner-controlled")
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	lock, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	info, err := lock.Stat()
	if err != nil || !os.SameFile(before, info) {
		_ = lock.Close()
		_ = root.Close()
		return nil, errors.New("file mailbox directory changed during open")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		_ = root.Close()
		return nil, err
	}
	return &FileMailbox{root: root, lock: lock}, nil
}

func (m *FileMailbox) Close() error { return errors.Join(m.lock.Close(), m.root.Close()) }

func (m *FileMailbox) Claim() ([]byte, error) {
	if err := m.private(); err != nil {
		return nil, err
	}
	if _, err := m.root.Lstat("response.json"); err == nil {
		return nil, ErrMailboxResultPending
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := m.root.Lstat("processing.json"); errors.Is(err, os.ErrNotExist) {
		// Validate before claiming so invalid filesystem objects remain untouched.
		if _, err := m.read("request.json"); err != nil {
			return nil, err
		}
		if err := m.root.Rename("request.json", "processing.json"); err != nil {
			return nil, err
		}
		if err := m.lock.Sync(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return m.read("processing.json")
}

func (m *FileMailbox) read(name string) ([]byte, error) {
	f, err := m.root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !unixPathOwner(info) || !ok || stat.Nlink != 1 || info.Size() < 1 || info.Size() > MaxRequestFrameBytes {
		return nil, errors.New("file mailbox input must be bounded private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRequestFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxRequestFrameBytes {
		return nil, errors.New("file mailbox input outside frame bounds")
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	return data, nil
}

// Publish syncs a complete bounded result before removing the claimed input.
// Existing results are never overwritten. A failed publication keeps the input
// available for stable-intent journal replay by the authenticated caller.
func (m *FileMailbox) Publish(data []byte) error {
	if err := m.private(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxResponseFrameBytes {
		return errors.New("file mailbox result outside frame bounds")
	}
	if _, err := m.read("processing.json"); err != nil {
		return err
	}
	// A fixed exclusive temporary name keeps the directory's footprint bounded.
	// A previous interrupted write is never accepted as a completed response.
	if info, err := m.root.Lstat("response.tmp"); err == nil {
		if !info.Mode().IsRegular() || !unixPathOwner(info) || info.Mode().Perm() != 0600 {
			return errors.New("file mailbox temporary result is not owner-controlled")
		}
		if err := m.root.Remove("response.tmp"); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := m.root.OpenFile("response.tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err := m.root.Link("response.tmp", "response.json"); err != nil {
		return err
	}
	if err := m.root.Remove("response.tmp"); err != nil {
		return err
	}
	if err := m.lock.Sync(); err != nil {
		return err
	}
	if err := m.root.Remove("processing.json"); err != nil {
		return err
	}
	return m.lock.Sync()
}

func (m *FileMailbox) private() error {
	info, err := m.lock.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || !unixPathOwner(info) {
		return errors.New("file mailbox directory lost private ownership")
	}
	return nil
}
