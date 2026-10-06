package adapter

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

func socketEndpointsUnnamed(fd int, _, _ string) (bool, error) {
	// Linux's decoded name "@" represents both an unnamed socket and an
	// empty abstract name. Only the family-only kernel address is unnamed.
	for _, call := range []uintptr{unix.SYS_GETSOCKNAME, unix.SYS_GETPEERNAME} {
		var address unix.RawSockaddrUnix
		length := uint32(unsafe.Sizeof(address))
		_, _, errno := unix.Syscall(call, uintptr(fd), uintptr(unsafe.Pointer(&address)), uintptr(unsafe.Pointer(&length)))
		if errno != 0 {
			return false, errno
		}
		if address.Family != unix.AF_UNIX || length != uint32(unsafe.Offsetof(address.Path)) {
			return false, nil
		}
	}
	return true, nil
}
