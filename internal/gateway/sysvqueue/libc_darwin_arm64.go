//go:build darwin && arm64

package sysvqueue

import (
	"golang.org/x/sys/unix"
	_ "unsafe"
)

// These follow x/sys's Darwin libSystem trampoline convention, including its
// runtime syscall entry points. No cgo build or deprecated raw syscall is used.
//
//go:linkname libcCall3 syscall.syscall
//go:uintptrescapes
func libcCall3(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err unix.Errno)

//go:linkname libcCall6 syscall.syscall6
//go:uintptrescapes
func libcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)

var libcMsgget, libcMsgctl, libcMsgsnd, libcMsgrcv uintptr

//go:cgo_import_dynamic libc_msgget msgget "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_msgctl msgctl "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_msgsnd msgsnd "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_msgrcv msgrcv "/usr/lib/libSystem.B.dylib"
