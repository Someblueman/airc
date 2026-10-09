//go:build linux && arm64

package adapter

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	sctpOptionPRSupported     = 113
	sctpOptionEvent           = 127
	sctpPartialDeliveryEvent  = 0x8006
	sctpPRInfoCmsg            = 5
	sctpPRPolicyTTL           = 0x0010
	sctpRecordTTLMilliseconds = 1000
)

func validSCTPMode(mode SCTPMode) bool {
	return mode == SCTPOrdered || mode == SCTPUnordered || mode == SCTPOrderedTTL
}

func configureSCTPSocket(fd int, listener bool, mode SCTPMode) error {
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, sctpOptionReceiveInfo, 1); err != nil {
		return fmt.Errorf("enable sctp receive metadata: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, sctpOptionFragmentInterleave, 0); err != nil {
		return fmt.Errorf("disable sctp fragment interleave: %w", err)
	}
	if listener {
		// Linux UAPI struct sctp_initmsg is four native-endian uint16 values.
		var init [8]byte
		binary.LittleEndian.PutUint16(init[0:2], 1)
		binary.LittleEndian.PutUint16(init[2:4], 1)
		if err := unix.SetsockoptString(fd, unix.IPPROTO_SCTP, sctpOptionInitMessage, string(init[:])); err != nil {
			return fmt.Errorf("configure one-stream sctp association: %w", err)
		}
	}
	if mode != SCTPOrderedTTL {
		return nil
	}
	if listener {
		// sctp_assoc_value: association zero selects future associations here.
		var support [8]byte
		binary.LittleEndian.PutUint32(support[4:8], 1)
		if err := unix.SetsockoptString(fd, unix.IPPROTO_SCTP, sctpOptionPRSupported, string(support[:])); err != nil {
			return fmt.Errorf("enable sctp timed reliability: %w", err)
		}
	}
	// A partial-delivery abort must reach readRecord as a notification, causing
	// association closure before any buffered prefix can become a request.
	var event [8]byte // sctp_event: association, event type, enabled, padding.
	binary.LittleEndian.PutUint16(event[4:6], sctpPartialDeliveryEvent)
	event[6] = 1
	if err := unix.SetsockoptString(fd, unix.IPPROTO_SCTP, sctpOptionEvent, string(event[:])); err != nil {
		return fmt.Errorf("enable sctp partial-delivery notifications: %w", err)
	}
	return nil
}

func sctpPRSupported(fd int) (bool, error) {
	// On an accepted one-to-one socket, association zero selects its peer.
	var value [8]byte
	length := uint32(len(value))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_SCTP,
		sctpOptionPRSupported, uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&length)), 0)
	if errno != 0 {
		return false, errno
	}
	if length != uint32(len(value)) {
		return false, errors.New("invalid sctp partial-reliability option length")
	}
	return binary.LittleEndian.Uint32(value[4:8]) == 1, nil
}

func sctpSendInfo(mode SCTPMode) []byte {
	if mode == SCTPOrdered {
		return nil
	}
	// Linux ARM64 cmsghdr is size_t + two int32 values. SNDINFO is 16 bytes;
	// PRINFO is uint16 policy, two padding bytes, then uint32 milliseconds.
	const sndInfoSize = 16
	size := unix.CmsgSpace(sndInfoSize)
	if mode == SCTPOrderedTTL {
		size += unix.CmsgSpace(8)
	}
	oob := make([]byte, size)
	binary.LittleEndian.PutUint64(oob[0:8], uint64(unix.CmsgLen(sndInfoSize)))
	binary.LittleEndian.PutUint32(oob[8:12], unix.IPPROTO_SCTP)
	binary.LittleEndian.PutUint32(oob[12:16], sctpSendInfoCmsg)
	if mode == SCTPUnordered {
		binary.LittleEndian.PutUint16(oob[unix.CmsgLen(0)+2:unix.CmsgLen(0)+4], sctpMessageUnordered)
	}
	if mode == SCTPOrderedTTL {
		control := oob[unix.CmsgSpace(sndInfoSize):]
		binary.LittleEndian.PutUint64(control[0:8], uint64(unix.CmsgLen(8)))
		binary.LittleEndian.PutUint32(control[8:12], unix.IPPROTO_SCTP)
		binary.LittleEndian.PutUint32(control[12:16], sctpPRInfoCmsg)
		data := control[unix.CmsgLen(0):]
		binary.LittleEndian.PutUint16(data[0:2], sctpPRPolicyTTL)
		binary.LittleEndian.PutUint32(data[4:8], sctpRecordTTLMilliseconds)
	}
	return oob
}
