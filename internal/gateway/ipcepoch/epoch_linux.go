//go:build linux && (arm64 || amd64)

package ipcepoch

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
)

// Current refuses substituted non-proc/non-namespace filesystems.
func Current() (Epoch, error) {
	var epoch Epoch
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
		return epoch, ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(file, 38))
	if err != nil {
		return epoch, err
	}
	boot := strings.TrimSuffix(string(body), "\n")
	if !validBootID(boot) {
		return epoch, ErrInvalid
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
		return epoch, ErrInvalid
	}
	var stat unix.Stat_t
	if err := unix.Fstat(namespace, &stat); err != nil {
		return epoch, err
	}
	if stat.Ino == 0 {
		return epoch, ErrInvalid
	}
	return Epoch{BootID: boot, NamespaceDevice: uint64(stat.Dev), NamespaceInode: stat.Ino}, nil
}
