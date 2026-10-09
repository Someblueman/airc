//go:build darwin && arm64

package sharedmem

import (
	"golang.org/x/sys/unix"
	_ "unsafe"
)

// The declarations follow the repository's existing libSystem trampoline
// convention; they avoid cgo and raw deprecated syscall numbers.
//
//go:linkname libcCall3 syscall.syscall
//go:uintptrescapes
func libcCall3(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err unix.Errno)

var libcSHMOpen uintptr

//go:cgo_import_dynamic libc_shm_open shm_open "/usr/lib/libSystem.B.dylib"
