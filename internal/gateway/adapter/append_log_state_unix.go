//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func privateAppendState(info os.FileInfo, limit int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && unixPathOwner(info) && ok && stat.Nlink == 1 && info.Size() <= int64(limit)
}

func (m *AppendLogMailbox) readState(name string, limit int) ([]byte, error) {
	f, err := m.reader.files.root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if name == "claim.json" && !privateAppendState(info, limit) {
		if err := m.recoverClaimLink(info, limit); err != nil {
			return nil, err
		}
		info, err = f.Stat()
		if err != nil {
			return nil, err
		}
	}
	if !privateAppendState(info, limit) || info.Size() == 0 {
		return nil, errors.New("append log state must be bounded private single-link regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > limit {
		clear(data)
		return nil, errors.New("append log state exceeds bound")
	}
	current, err := f.Stat()
	pathInfo, pathErr := m.reader.files.root.Lstat(name)
	if err != nil || pathErr != nil || !privateAppendState(current, limit) || !os.SameFile(current, pathInfo) || current.Size() != int64(len(data)) {
		clear(data)
		return nil, errors.New("append log state changed during read")
	}
	if err := f.Sync(); err != nil {
		clear(data)
		return nil, err
	}
	return data, nil
}

// Fixed private temporary slots bound interrupted writes. Claims/responses use
// exclusive links; only the validated cursor is replaced by atomic rename.
func (m *AppendLogMailbox) stage(name string, data []byte) error {
	limit, replace := 0, false
	switch name {
	case "claim.json":
		limit = maxAppendClaimBytes
	case "cursor.json":
		limit, replace = 1024, true
	case "response.json":
		limit = MaxResponseFrameBytes
	default:
		return errors.New("invalid append log state slot")
	}
	if len(data) == 0 || len(data) > limit {
		return errors.New("append log publication exceeds bound")
	}
	root := m.reader.files.root
	if info, err := root.Lstat(name); err == nil {
		if !privateAppendState(info, limit) {
			return errors.New("unsafe append log state target")
		}
		if !replace {
			return ErrMailboxResultPending
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := name + ".tmp"
	if info, err := root.Lstat(temporary); err == nil {
		if !privateAppendState(info, limit) {
			return errors.New("unsafe append log temporary state")
		}
		if err := root.Remove(temporary); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if replace {
		if err := root.Rename(temporary, name); err != nil {
			return err
		}
	} else {
		if err := root.Link(temporary, name); err != nil {
			return err
		}
		if err := root.Remove(temporary); err != nil {
			return err
		}
	}
	return m.reader.files.syncDirectory(".")
}

// Recover only the two fixed names created by exclusive claim publication.
// Sync the completed name before retiring its temporary alias.
func (m *AppendLogMailbox) recoverClaimLink(info os.FileInfo, limit int) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	root := m.reader.files.root
	temporary, err := root.Lstat("claim.json.tmp")
	if !ok || stat.Nlink != 2 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !unixPathOwner(info) || info.Size() <= 0 || info.Size() > int64(limit) || err != nil || !os.SameFile(info, temporary) || temporary.Mode() != info.Mode() {
		return errors.New("unsafe append log claim links")
	}
	if err := m.reader.files.syncDirectory("."); err != nil {
		return err
	}
	if err := root.Remove("claim.json.tmp"); err != nil {
		return err
	}
	return m.reader.files.syncDirectory(".")
}
