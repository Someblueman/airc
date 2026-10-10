//go:build darwin && arm64

package sharedmem

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openNative(name string) (int, error) {
	cname := append([]byte(name), 0)
	r1, _, errno := libcCall3(libcSHMOpen, uintptr(unsafe.Pointer(&cname[0])), uintptr(unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW), 0)
	runtime.KeepAlive(cname)
	if errno != 0 {
		return -1, errno
	}
	return int(r1), nil
}
