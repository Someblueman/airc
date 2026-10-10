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
	switch st.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
		return nil
	default:
		return errors.New("journal filesystem is not a supported local type")
	}
}
