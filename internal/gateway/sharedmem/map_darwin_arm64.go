//go:build darwin && arm64

package sharedmem

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func checkNative(fd, size int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("inspect shared memory: %w", err)
	}
	if st.Uid != uint32(os.Geteuid()) || uint32(st.Mode)&07777 != 0600 || st.Size != int64(size) {
		return ErrInvalid
	}
	return nil
}

func mapNative(fd, size int) ([]byte, error) {
	if err := checkNative(fd, size); err != nil {
		return nil, err
	}
	return unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
}

func unmapNative(view []byte) error { return unix.Munmap(view) }

func closeNative(fd int) error { return unix.Close(fd) }
