//go:build linux && arm64

package gateway_test

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"golang.org/x/sys/unix"
)

const (
	sctpTestPRInfo      = 5
	sctpTestPRSupported = 113
	sctpTestTTLPolicy   = 0x0010
	sctpTestTTLMillis   = 1000
)

func sctpSendControl(mode adapter.SCTPMode, partialReliable bool) []byte {
	size := unix.CmsgSpace(sctpTestSndInfoBytes)
	if partialReliable {
		size += unix.CmsgSpace(8)
	}
	oob := make([]byte, size)
	setCmsg := func(offset, typ int, data []byte) int {
		hdr := (*unix.Cmsghdr)(unsafe.Pointer(&oob[offset]))
		hdr.Level, hdr.Type = unix.IPPROTO_SCTP, int32(typ)
		hdr.SetLen(unix.CmsgLen(len(data)))
		start := offset + unix.CmsgLen(0)
		copy(oob[start:start+len(data)], data)
		return offset + unix.CmsgSpace(len(data))
	}
	data := make([]byte, sctpTestSndInfoBytes)
	// sctp_sndinfo: stream 0, ordered, PPID 0, no explicit association.
	if mode == adapter.SCTPUnordered {
		binary.LittleEndian.PutUint16(data[2:4], sctpTestUnordered)
	}
	offset := setCmsg(0, sctpTestSndInfo, data)
	if partialReliable {
		prinfo := make([]byte, 8) // uint16 policy, two-byte padding, uint32 TTL.
		binary.LittleEndian.PutUint16(prinfo[0:2], sctpTestTTLPolicy)
		binary.LittleEndian.PutUint32(prinfo[4:8], sctpTestTTLMillis)
		setCmsg(offset, sctpTestPRInfo, prinfo)
	}
	return oob
}

func setSCTPPRSupport(fd int) error {
	var value [8]byte // sctp_assoc_value: association 0, enabled.
	binary.LittleEndian.PutUint32(value[4:8], 1)
	return unix.SetsockoptString(fd, unix.IPPROTO_SCTP, sctpTestPRSupported, string(value[:]))
}

func querySCTPPRSupport(fd int) (bool, error) {
	var value [8]byte
	length := uint32(len(value))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_SCTP,
		sctpTestPRSupported, uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&length)), 0)
	if errno != 0 {
		return false, errno
	}
	if length != uint32(len(value)) {
		return false, fmt.Errorf("SCTP_PR_SUPPORTED length=%d want=%d", length, len(value))
	}
	return binary.LittleEndian.Uint32(value[4:8]) == 1, nil
}
