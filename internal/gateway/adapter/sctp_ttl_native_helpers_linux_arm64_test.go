//go:build linux && arm64

package adapter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const sctpTestOptionPRAssocStatus = 115
const sctpTestStatusSize = 176

func sctpPRAbandonStats(t *testing.T, conn *sctpConn) (unsent, sent uint64) {
	t.Helper()
	var callErr error
	err := conn.raw.Control(func(fd uintptr) {
		var status [256]byte
		length := uint32(len(status))
		_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, fd, unix.IPPROTO_SCTP, 14,
			uintptr(unsafe.Pointer(&status[0])), uintptr(unsafe.Pointer(&length)), 0)
		if errno != 0 {
			callErr = errno
			return
		}
		if length != sctpTestStatusSize {
			callErr = fmt.Errorf("SCTP_STATUS length=%d, want %d", length, sctpTestStatusSize)
			return
		}
		assoc := binary.LittleEndian.Uint32(status[:4])
		var pr [24]byte
		binary.LittleEndian.PutUint32(pr[:4], assoc)
		binary.LittleEndian.PutUint16(pr[6:8], sctpPRPolicyTTL)
		length = uint32(len(pr))
		_, _, errno = unix.Syscall6(unix.SYS_GETSOCKOPT, fd, unix.IPPROTO_SCTP, sctpTestOptionPRAssocStatus,
			uintptr(unsafe.Pointer(&pr[0])), uintptr(unsafe.Pointer(&length)), 0)
		if errno != 0 {
			callErr = errno
			return
		}
		if length != uint32(len(pr)) {
			callErr = fmt.Errorf("PR_ASSOC_STATUS length=%d, want %d", length, len(pr))
			return
		}
		unsent = binary.LittleEndian.Uint64(pr[8:16])
		sent = binary.LittleEndian.Uint64(pr[16:24])
		runtime.KeepAlive(&status)
		runtime.KeepAlive(&pr)
	})
	if err != nil {
		t.Fatal(err)
	}
	if callErr != nil {
		t.Fatal(callErr)
	}
	return unsent, sent
}

func waitSCTPAbandonedSent(t *testing.T, conn *sctpConn, baseline uint64) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_, sent := sctpPRAbandonStats(t, conn)
		if sent > baseline {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	_, sent := sctpPRAbandonStats(t, conn)
	t.Fatalf("SCTP abandoned_sent=%d did not advance beyond %d", sent, baseline)
}

func receiveSCTPPartialAbort(t *testing.T, peer *sctpTestPeer) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		poll := []unix.PollFd{{Fd: int32(peer.file.Fd()), Events: unix.POLLIN}}
		if _, err := unix.Poll(poll, 100); err != nil {
			t.Fatal(err)
		}
		if poll[0].Revents == 0 {
			continue
		}
		data := make([]byte, sctpRecordLimit)
		oob := make([]byte, 256)
		n, _, flags, _, err := unix.Recvmsg(int(peer.file.Fd()), data, oob, unix.MSG_DONTWAIT)
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			t.Fatalf("partial abort receive flags=0x%x", flags)
		}
		if flags&sctpNotificationFlag != 0 {
			if n < 12 || binary.LittleEndian.Uint16(data[:2]) != sctpPartialDeliveryEvent || binary.LittleEndian.Uint32(data[8:12]) != 0 {
				t.Fatalf("unexpected SCTP notification: %x", data[:n])
			}
			return
		}
		if n == 0 || flags&unix.MSG_EOR != 0 {
			t.Fatalf("partial message unexpectedly completed: bytes=%d flags=0x%x", n, flags)
		}
	}
	t.Fatal("partial-delivery-aborted notification not received")
}

func receiveSCTPWholeRecord(t *testing.T, peer *sctpTestPeer, want []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		ms := int((remaining + time.Millisecond - 1) / time.Millisecond)
		poll := []unix.PollFd{{Fd: int32(peer.file.Fd()), Events: unix.POLLIN}}
		if _, err := unix.Poll(poll, ms); err != nil {
			t.Fatal(err)
		}
		if poll[0].Revents == 0 {
			continue
		}
		data := make([]byte, sctpWriteRecordLimit)
		oob := make([]byte, unix.CmsgSpace(sctpReceiveInfoSize))
		n, oobn, flags, _, err := unix.Recvmsg(int(peer.file.Fd()), data, oob, unix.MSG_DONTWAIT)
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC|sctpNotificationFlag) != 0 || flags&unix.MSG_EOR == 0 {
			t.Fatalf("recovery record flags=0x%x bytes=%d", flags, n)
		}
		if !bytes.Equal(data[:n], want) {
			t.Fatalf("recovery record=%q, want %q", data[:n], want)
		}
		if _, err := parseSCTPReceiveInfo(oob[:oobn], SCTPOrderedTTL); err != nil {
			t.Fatalf("recovery metadata: %v", err)
		}
		return
	}
	t.Fatal("complete recovery record did not arrive")
}

type sctpPauseRawConn struct {
	syscall.RawConn
	once  sync.Once
	pause time.Duration
}

func (c *sctpPauseRawConn) Read(fn func(uintptr) bool) error {
	var received bool
	err := c.RawConn.Read(func(fd uintptr) bool {
		ready := fn(fd)
		if ready {
			received = true
		}
		return ready
	})
	if err == nil && received {
		c.once.Do(func() { time.Sleep(c.pause) })
	}
	return err
}
