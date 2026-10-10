//go:build darwin && arm64

package adapter

import (
	"bytes"
	"errors"

	"golang.org/x/sys/unix"
)

func mappedFileFilesystemAllowed(fd int) error {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(fd, &stat); err != nil {
		return mappedFileError("inspect mapped queue filesystem", err)
	}
	end := bytes.IndexByte(stat.Fstypename[:], 0)
	if end < 0 {
		end = len(stat.Fstypename)
	}
	switch string(stat.Fstypename[:end]) {
	case "apfs", "hfs":
		return nil
	default:
		return errors.Join(ErrMappedQueueInvalid, errors.New("mapped queue requires local APFS or HFS"))
	}
}
