//go:build linux && (arm64 || amd64)

package adapter

import (
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// CurrentPOSIXQueueEpoch refuses substituted non-proc/non-namespace filesystems.
func CurrentPOSIXQueueEpoch() (POSIXQueueEpoch, error) {
	var epoch POSIXQueueEpoch
	fd, err := unix.Open("/proc/sys/kernel/random/boot_id", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return epoch, err
	}
	file := os.NewFile(uintptr(fd), "boot_id")
	defer file.Close()
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return epoch, err
	}
	if fs.Type != unix.PROC_SUPER_MAGIC {
		return epoch, ErrPOSIXQueueInvalid
	}
	body, err := io.ReadAll(io.LimitReader(file, 38))
	if err != nil {
		return epoch, err
	}
	boot := strings.TrimSuffix(string(body), "\n")
	if len(boot) != 36 {
		return epoch, ErrPOSIXQueueInvalid
	}
	for i, c := range boot {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return epoch, ErrPOSIXQueueInvalid
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return epoch, ErrPOSIXQueueInvalid
		}
	}
	// /proc/self/ns/ipc is a kernel namespace link; following it yields nsfs.
	namespace, err := unix.Open("/proc/self/ns/ipc", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return epoch, err
	}
	defer unix.Close(namespace)
	if err := unix.Fstatfs(namespace, &fs); err != nil {
		return epoch, err
	}
	if fs.Type != unix.NSFS_MAGIC {
		return epoch, ErrPOSIXQueueInvalid
	}
	var stat unix.Stat_t
	if err := unix.Fstat(namespace, &stat); err != nil {
		return epoch, err
	}
	if stat.Ino == 0 {
		return epoch, ErrPOSIXQueueInvalid
	}
	return POSIXQueueEpoch{BootID: boot, NamespaceDevice: uint64(stat.Dev), NamespaceInode: stat.Ino}, nil
}

// ResumePOSIXQueue acquires unlink ownership only for the exact recorded inode
// in its original live boot/IPC namespace. The caller must hold exclusive journal
// ownership. Unknown queues, stale epochs and replacements are never adopted.
func ResumePOSIXQueue(name string, depth int, identity POSIXQueueIdentity, epoch POSIXQueueEpoch) (*POSIXQueue, error) {
	current, err := CurrentPOSIXQueueEpoch()
	if err != nil {
		return nil, err
	}
	if current != epoch || identity.Inode == 0 {
		return nil, ErrPOSIXQueueInvalid
	}
	queue, err := OpenPOSIXQueue(name, depth)
	if err != nil {
		return nil, err
	}
	current, err = CurrentPOSIXQueueEpoch()
	if err != nil || current != epoch || queue.Identity() != identity {
		return nil, errors.Join(ErrPOSIXQueueInvalid, err, queue.Close())
	}
	queue.owned = true
	return queue, nil
}
