//go:build linux && arm64

package gateway_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	sctpTestStatusOption        = 14
	sctpTestPRAssocStatusOption = 115
	sctpTestPolicyTTL           = 0x0010
)

func sctpTestGetOption(fd, option int, value []byte) (uint32, error) {
	if len(value) == 0 {
		return 0, errors.New("empty SCTP option buffer")
	}
	length := uint32(len(value))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_SCTP,
		uintptr(option), uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&length)), 0)
	if errno != 0 {
		return 0, errno
	}
	return length, nil
}

func sctpTestAssociationStatus(fd int) (int32, uint32, error) {
	var value [256]byte // Linux UAPI struct sctp_status.
	length, err := sctpTestGetOption(fd, sctpTestStatusOption, value[:])
	if err != nil {
		return 0, 0, fmt.Errorf("getsockopt SCTP_STATUS: %w", err)
	}
	if length < 12 {
		return 0, 0, fmt.Errorf("SCTP_STATUS length=%d shorter than association/rwnd fields", length)
	}
	return int32(binary.LittleEndian.Uint32(value[0:4])), binary.LittleEndian.Uint32(value[8:12]), nil
}

func sctpTestPRAssocStatus(fd int, association int32) (uint64, uint64, error) {
	var value [24]byte // Linux UAPI struct sctp_prstatus.
	binary.LittleEndian.PutUint32(value[0:4], uint32(association))
	binary.LittleEndian.PutUint16(value[4:6], 0) // stream 0
	binary.LittleEndian.PutUint16(value[6:8], sctpTestPolicyTTL)
	length, err := sctpTestGetOption(fd, sctpTestPRAssocStatusOption, value[:])
	if err != nil {
		return 0, 0, fmt.Errorf("getsockopt SCTP_PR_ASSOC_STATUS: %w", err)
	}
	if length != uint32(len(value)) {
		return 0, 0, fmt.Errorf("SCTP_PR_ASSOC_STATUS length=%d want=%d", length, len(value))
	}
	return binary.LittleEndian.Uint64(value[8:16]), binary.LittleEndian.Uint64(value[16:24]), nil
}

func waitSCTPPeerWindowDrop(fd int, previous uint32, deadline time.Time) (uint32, error) {
	for time.Now().Before(deadline) {
		_, window, err := sctpTestAssociationStatus(fd)
		if err != nil {
			return 0, err
		}
		if window < previous {
			return window, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, window, err := sctpTestAssociationStatus(fd)
	if err != nil {
		return 0, err
	}
	return window, fmt.Errorf("SCTP peer receiver window remained %d after bounded reliable fill (previous %d)", window, previous)
}

func waitSCTPPRAbandonment(t *testing.T, fd int, association int32, beforeUnsent, beforeSent uint64, deadline time.Time) error {
	t.Helper()
	for time.Now().Before(deadline) {
		unsent, sent, err := sctpTestPRAssocStatus(fd, association)
		if err != nil {
			return err
		}
		if unsent+sent > beforeUnsent+beforeSent {
			t.Logf("SCTP_PR_ASSOC_STATUS abandonment witness: unsent %d->%d sent %d->%d", beforeUnsent, unsent, beforeSent, sent)
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	unsent, sent, err := sctpTestPRAssocStatus(fd, association)
	if err != nil {
		return err
	}
	return fmt.Errorf("PR-SCTP counters did not advance: unsent %d->%d sent %d->%d", beforeUnsent, unsent, beforeSent, sent)
}

func sctpPadRequestToLimit(t *testing.T, wire []byte) []byte {
	return sctpPadRequestToSize(t, wire, sctpTestMessageLimit)
}

func sctpPadRequestToSize(t *testing.T, wire []byte, size int) []byte {
	t.Helper()
	if size <= 0 || len(wire) >= size || size > sctpTestMessageLimit {
		t.Fatalf("padded SCTP request starts at %d bytes, cannot reach size %d within limit %d", len(wire), size, sctpTestMessageLimit)
	}
	return append(wire, []byte(strings.Repeat(" ", size-len(wire)))...)
}

func waitSCTPGatewayStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			t.Fatalf("read stopped gateway state: %v", err)
		}
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "State:") && strings.Contains(line, "T (stopped)") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway did not enter the stopped state")
}
