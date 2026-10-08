//go:build darwin && arm64

package sysvqueue

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	"unsafe"
)

// Darwin ARM64's packed msqid_ds is 116 bytes. Offsets are checked against the
// installed native C headers and actual IPC_STAT for this selected kernel profile.
func (q *Queue) inspect() (Identity, error) {
	var raw [116]byte
	_, _, errno := libcCall3(libcMsgctl, uintptr(q.identity.ID), unix.IPC_STAT, uintptr(unsafe.Pointer(&raw)))
	runtime.KeepAlive(&raw)
	if errno != 0 {
		return Identity{}, errno
	}
	owner := uint32(os.Geteuid())
	uid, cuid := binary.LittleEndian.Uint32(raw[0:4]), binary.LittleEndian.Uint32(raw[8:12])
	mode := binary.LittleEndian.Uint16(raw[16:18])
	key := int32(binary.LittleEndian.Uint32(raw[20:24]))
	capacity := binary.LittleEndian.Uint64(raw[48:56])
	if uid != owner || cuid != owner || mode&07777 != 0600 || key != q.identity.Key || capacity < MessageBytes || capacity > 65536 {
		return Identity{}, ErrInvalid
	}
	return Identity{ID: q.identity.ID, Key: key, Sequence: binary.LittleEndian.Uint16(raw[18:20]), Changed: int64(binary.LittleEndian.Uint64(raw[88:96]))}, nil
}
func nativeGet(key, flags uintptr) (uintptr, uintptr, unix.Errno) {
	return libcCall3(libcMsgget, key, flags, 0)
}

//go:uintptrescapes
func nativeControl(id, cmd, data uintptr) (uintptr, uintptr, unix.Errno) {
	return libcCall3(libcMsgctl, id, cmd, data)
}

//go:uintptrescapes
func nativeSend(id, data, size uintptr) (uintptr, uintptr, unix.Errno) {
	return libcCall6(libcMsgsnd, id, data, size, unix.IPC_NOWAIT, 0, 0)
}

//go:uintptrescapes
func nativeReceive(id, data, size uintptr) (uintptr, uintptr, unix.Errno) {
	return libcCall6(libcMsgrcv, id, data, size, 0, unix.IPC_NOWAIT, 0)
}
