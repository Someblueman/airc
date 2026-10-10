//go:build linux && arm64

package adapter

import (
	"errors"

	"golang.org/x/sys/unix"
)

const (
	linuxExt4SuperMagic  = 0xEF53
	linuxXfsSuperMagic   = 0x58465342
	linuxBtrfsSuperMagic = 0x9123683E
)

func mappedFileFilesystemAllowed(fd int) error {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(fd, &stat); err != nil {
		return mappedFileError("inspect mapped queue filesystem", err)
	}
	switch uint64(stat.Type) {
	case linuxExt4SuperMagic, linuxXfsSuperMagic, linuxBtrfsSuperMagic:
		return nil
	default:
		return errors.Join(ErrMappedQueueInvalid, errors.New("mapped queue requires local ext4, XFS or Btrfs"))
	}
}
