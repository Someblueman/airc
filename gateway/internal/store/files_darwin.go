package store

import (
	"errors"

	"golang.org/x/sys/unix"
)

func localFilesystem(fd int) error {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return err
	}
	if st.Flags&unix.MNT_LOCAL == 0 {
		return errors.New("journal requires local filesystem")
	}
	return nil
}
