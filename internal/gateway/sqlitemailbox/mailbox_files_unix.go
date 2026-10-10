//go:build darwin || linux

package sqlitemailbox

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openMailboxFiles(path string) (*os.File, *os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, ErrRequest
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, nil, ErrRequest
	}
	if err := safeAncestors(path); err != nil {
		return nil, nil, err
	}
	dfd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, ErrRequest
	}
	directory := os.NewFile(uintptr(dfd), path)
	fail := func(db *os.File) (*os.File, *os.File, error) {
		if db != nil {
			_ = db.Close()
		}
		_ = directory.Close()
		return nil, nil, ErrRequest
	}
	var ds unix.Stat_t
	if err := unix.Fstat(dfd, &ds); err != nil || ds.Uid != uint32(os.Geteuid()) || ds.Mode&unix.S_IFMT != unix.S_IFDIR || ds.Mode&07777 != 0700 {
		return fail(nil)
	}
	if err := localFilesystem(dfd); err != nil {
		return fail(nil)
	}
	if err := unix.Flock(dfd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(nil)
	}
	dbFD, err := unix.Openat(dfd, "mailbox.sqlite", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return fail(nil)
	}
	database := os.NewFile(uintptr(dbFD), filepath.Join(path, "mailbox.sqlite"))
	var st unix.Stat_t
	if err := unix.Fstat(dbFD, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size < 100 || st.Size > MaxDatabaseBytes {
		return fail(database)
	}
	if err := localFilesystem(dbFD); err != nil {
		return fail(database)
	}
	header := make([]byte, 16)
	if n, err := database.ReadAt(header, 0); err != nil || n != len(header) || string(header) != "SQLite format 3\x00" {
		return fail(database)
	}
	if err := checkSidecars(dfd); err != nil {
		return fail(database)
	}
	return directory, database, nil
}

func safeAncestors(path string) error {
	uid := uint32(os.Geteuid())
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		var sys unix.Stat_t
		if err := unix.Lstat(current, &sys); err != nil || sys.Mode&unix.S_IFMT != unix.S_IFDIR {
			return ErrRequest
		}
		if sys.Uid != uid && sys.Uid != 0 {
			return ErrRequest
		}
		if sys.Mode&0022 != 0 && !(sys.Uid == 0 && sys.Mode&01000 != 0) {
			return ErrRequest
		}
		if current == "/" {
			break
		}
	}
	return nil
}

func verifyMailboxPaths(path string, directory, database *os.File) error {
	if directory == nil || database == nil {
		return ErrClosed
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return ErrClosed
	}
	if err := safeAncestors(path); err != nil {
		return ErrClosed
	}
	dirFD, dbFD := int(directory.Fd()), int(database.Fd())
	var pinnedDir, currentDir, pinnedDB, currentDB unix.Stat_t
	if unix.Fstat(dirFD, &pinnedDir) != nil || unix.Stat(path, &currentDir) != nil || pinnedDir.Dev != currentDir.Dev || pinnedDir.Ino != currentDir.Ino || pinnedDir.Uid != uint32(os.Geteuid()) || pinnedDir.Mode&07777 != 0700 {
		return ErrClosed
	}
	if unix.Fstat(dbFD, &pinnedDB) != nil {
		return ErrClosed
	}
	if localFilesystem(dirFD) != nil || localFilesystem(dbFD) != nil {
		return ErrClosed
	}
	checkFD, err := unix.Openat(dirFD, "mailbox.sqlite", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrClosed
	}
	err = unix.Fstat(checkFD, &currentDB)
	_ = unix.Close(checkFD)
	if err != nil || pinnedDB.Dev != currentDB.Dev || pinnedDB.Ino != currentDB.Ino || currentDB.Uid != uint32(os.Geteuid()) || currentDB.Mode&unix.S_IFMT != unix.S_IFREG || currentDB.Mode&07777 != 0600 || currentDB.Nlink != 1 || currentDB.Size < 100 || currentDB.Size > MaxDatabaseBytes {
		return ErrClosed
	}
	if checkSidecars(dirFD) != nil {
		return ErrClosed
	}
	return nil
}

func checkSidecars(dirFD int) error {
	// DELETE-mode producers may have an active journal between polls, and a
	// hot journal must remain available for SQLite's own crash recovery.
	var journal unix.Stat_t
	err := unix.Fstatat(dirFD, "mailbox.sqlite-journal", &journal, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if journal.Mode&unix.S_IFMT != unix.S_IFREG || journal.Uid != uint32(os.Geteuid()) || journal.Mode&07777 != 0600 || journal.Nlink != 1 || journal.Size < 0 || journal.Size > 2*MaxDatabaseBytes {
			return ErrRequest
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return ErrRequest
	}
	for _, name := range []string{"mailbox.sqlite-wal", "mailbox.sqlite-shm"} {
		var st unix.Stat_t
		err := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil || !errors.Is(err, unix.ENOENT) {
			return ErrRequest
		}
	}
	return nil
}

func closeMailboxFiles(directory, database *os.File) error {
	var errs []error
	if database != nil {
		errs = append(errs, database.Close())
	}
	if directory != nil {
		_ = unix.Flock(int(directory.Fd()), unix.LOCK_UN)
		errs = append(errs, directory.Close())
	}
	return errors.Join(errs...)
}
