//go:build linux && arm64

package sharedmem

import (
	"golang.org/x/sys/unix"
)

func openNative(name string) (int, error) {
	dirfd, err := unix.Open("/dev/shm", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(dirfd)
	// Inspect without following symlinks before opening. O_NONBLOCK on the
	// subsequent open prevents a racing FIFO replacement from hanging startup;
	// mapNative verifies the opened descriptor's type and owner again.
	var before unix.Stat_t
	if err := unix.Fstatat(dirfd, name[1:], &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 {
		return -1, ErrInvalid
	}
	fd, err := unix.Openat(dirfd, name[1:], unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, err
	}
	return fd, nil
}
