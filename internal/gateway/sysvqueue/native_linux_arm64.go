//go:build linux && arm64

package sysvqueue

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	"unsafe"
)

// Linux ARM64 msqid64_ds follows the asm-generic UAPI layout. The offsets
// are also checked against actual IPC_STAT in the native Linux fixture.
func (q *Queue) inspect() (Identity, error) {
	var raw [120]byte
	_, _, errno := nativeControl(uintptr(q.identity.ID), unix.IPC_STAT, uintptr(unsafe.Pointer(&raw)))
	runtime.KeepAlive(&raw)
	if errno != 0 {
		return Identity{}, errno
	}
	owner := uint32(os.Geteuid())
	uid, cuid := binary.LittleEndian.Uint32(raw[4:8]), binary.LittleEndian.Uint32(raw[12:16])
	mode := binary.LittleEndian.Uint32(raw[20:24])
	key := int32(binary.LittleEndian.Uint32(raw[0:4]))
	capacity := binary.LittleEndian.Uint64(raw[88:96])
	if uid != owner || cuid != owner || mode&07777 != 0600 || key != q.identity.Key || capacity < MessageBytes || capacity > 65536 {
		return Identity{}, ErrInvalid
	}
	return Identity{ID: q.identity.ID, Key: key, Sequence: binary.LittleEndian.Uint16(raw[24:26]), Changed: int64(binary.LittleEndian.Uint64(raw[64:72]))}, nil
}
func nativeGet(key, flags uintptr) (uintptr, uintptr, unix.Errno) {
	return unix.Syscall(unix.SYS_MSGGET, key, flags, 0)
}

//go:uintptrescapes
func nativeControl(id, cmd, data uintptr) (uintptr, uintptr, unix.Errno) {
	return unix.Syscall(unix.SYS_MSGCTL, id, cmd, data)
}

//go:uintptrescapes
func nativeSend(id, data, size uintptr) (uintptr, uintptr, unix.Errno) {
	return unix.Syscall6(unix.SYS_MSGSND, id, data, size, unix.IPC_NOWAIT, 0, 0)
}

//go:uintptrescapes
func nativeReceive(id, data, size uintptr) (uintptr, uintptr, unix.Errno) {
	return unix.Syscall6(unix.SYS_MSGRCV, id, data, size, 0, unix.IPC_NOWAIT, 0)
}
