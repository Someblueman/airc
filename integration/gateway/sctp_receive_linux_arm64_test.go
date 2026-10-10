//go:build linux && arm64

package gateway_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"golang.org/x/sys/unix"
)

func (c *sctpTestConn) readRecord(t *testing.T, deadline time.Time) ([]byte, error) {
	t.Helper()
	message := make([]byte, 0, 4096)
	for {
		remaining := sctpTestResponseLimit - len(message)
		if remaining <= 0 {
			return nil, fmt.Errorf("SCTP response exceeded %d bytes", sctpTestResponseLimit-1)
		}
		fragment := make([]byte, remaining)
		oob := make([]byte, unix.CmsgSpace(sctpTestRcvInfoBytes))
		n, oobn, flags, _, err := unix.Recvmsg(c.fd, fragment, oob, unix.MSG_DONTWAIT)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			if err := c.wait(t, unix.POLLIN, deadline); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.EOF
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || flags&0x8000 != 0 {
			return nil, errors.New("SCTP response had truncation or notification flags")
		}
		// RCVINFO confirms ordering, stream, and PPID. Linux does not report
		// the sender's PRINFO TTL here; the client cmsg and expiry-status tests
		// establish that sender-side contract independently.
		if err := validateSCTPTestReceiveInfo(oob[:oobn], c.mode); err != nil {
			return nil, err
		}
		if len(message)+n > sctpTestResponseLimit-1 {
			return nil, fmt.Errorf("SCTP response exceeded %d bytes", sctpTestResponseLimit-1)
		}
		message = append(message, fragment[:n]...)
		if flags&unix.MSG_EOR != 0 {
			return message, nil
		}
	}
}

func validateSCTPTestReceiveInfo(oob []byte, mode adapter.SCTPMode) error {
	control, err := unix.ParseSocketControlMessage(oob)
	if err != nil || len(control) != 1 {
		return fmt.Errorf("SCTP receive metadata count=%d err=%v", len(control), err)
	}
	message := control[0]
	if message.Header.Level != unix.IPPROTO_SCTP || message.Header.Type != sctpTestRcvInfo || len(message.Data) != sctpTestRcvInfoBytes {
		return errors.New("SCTP response lacked the expected RCVINFO metadata")
	}
	stream := binary.LittleEndian.Uint16(message.Data[:2])
	flags := binary.LittleEndian.Uint16(message.Data[4:6])
	ppid := binary.BigEndian.Uint32(message.Data[8:12])
	wantFlags := uint16(0)
	if mode == adapter.SCTPUnordered {
		wantFlags = sctpTestUnordered
	}
	if stream != 0 || flags != wantFlags || ppid != 0 {
		return fmt.Errorf("SCTP response stream=%d flags=%d ppid=%d; want stream 0 flags %d PPID 0", stream, flags, ppid, wantFlags)
	}
	return nil
}

func (c *sctpTestConn) wait(t *testing.T, events int16, deadline time.Time) error {
	t.Helper()
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return os.ErrDeadlineExceeded
		}
		ms := int(remaining.Milliseconds())
		if ms < 1 {
			ms = 1
		}
		poll := []unix.PollFd{{Fd: int32(c.fd), Events: events}}
		n, err := unix.Poll(poll, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return os.ErrDeadlineExceeded
		}
		revents := poll[0].Revents
		if revents&unix.POLLNVAL != 0 {
			return syscall.EBADF
		}
		if revents&(unix.POLLERR|unix.POLLHUP) != 0 && revents&events == 0 {
			return io.EOF
		}
		if revents&events != 0 {
			return nil
		}
	}
}
