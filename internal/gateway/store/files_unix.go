//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type files struct {
	directory, lock *os.File
	path            string
}

func openFiles(path string) (*files, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("invalid state directory")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := &files{directory: os.NewFile(uintptr(fd), path), path: path}
	fail := func(err error) (*files, error) { _ = f.close(); return nil, err }
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail(err)
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		return fail(errors.New("state directory must be private and owned"))
	}
	if err := localFilesystem(fd); err != nil {
		return fail(err)
	}
	f.lock, err = f.file("owner.lock", true)
	if err != nil {
		return fail(err)
	}
	if err := unix.Flock(int(f.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(errors.New("journal already owned or locking unavailable"))
	}
	db, err := f.file("journal.sqlite", true)
	if err != nil {
		return fail(err)
	}
	if err := db.Close(); err != nil {
		return fail(err)
	}
	for _, name := range []string{"journal.sqlite-wal", "journal.sqlite-shm", "journal.sqlite-journal"} {
		file, err := f.file(name, false)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(err)
		}
		if err := file.Close(); err != nil {
			return fail(err)
		}
	}
	if err := f.directory.Sync(); err != nil {
		return fail(err)
	}
	return f, nil
}

func (f *files) file(name string, create bool) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Openat(int(f.directory.Fd()), name, flags, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, errors.New("journal file must be private owned regular and unaliased")
	}
	return os.NewFile(uintptr(fd), filepath.Join(f.path, name)), nil
}

func (f *files) close() error {
	var errs []error
	if f.lock != nil {
		errs = append(errs, f.lock.Close())
		f.lock = nil
	}
	if f.directory != nil {
		errs = append(errs, f.directory.Close())
		f.directory = nil
	}
	return errors.Join(errs...)
}

func (f *files) verify() error {
	for _, name := range []string{"journal.sqlite", "journal.sqlite-wal", "journal.sqlite-shm"} {
		file, err := f.file(name, false)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return f.directory.Sync()
}
