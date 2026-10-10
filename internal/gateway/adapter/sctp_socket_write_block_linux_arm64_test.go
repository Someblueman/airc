//go:build linux && arm64

package adapter

import (
	"errors"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSCTPWriteDeadlineAndCloseWakeBlockedWrites(t *testing.T) {
	for _, mode := range []SCTPMode{SCTPOrdered, SCTPUnordered} {
		t.Run(strconv.Itoa(int(mode)), func(t *testing.T) { testSCTPBlockedWrites(t, mode) })
	}
}

func testSCTPBlockedWrites(t *testing.T, mode SCTPMode) {
	listener, err := ListenSCTP("127.0.0.1:0", mode)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := sctpTestClientWithBuffers(t, listener.Addr(), 4096, 4096)
	defer client.close()
	accepted := acceptSCTPTestConn(t, listener).(*sctpConn)
	defer accepted.Close()
	var activeWrite <-chan error
	t.Cleanup(func() {
		if activeWrite == nil {
			return
		}
		_ = accepted.Close()
		select {
		case <-activeWrite:
		case <-time.After(time.Second):
			t.Error("cleanup could not join blocked SCTP Write")
		}
	})
	clientRaw, err := client.file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []syscall.RawConn{clientRaw, accepted.raw} {
		if err := sctpTestControl(raw, func(fd int) error {
			return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 4096)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sctpTestControl(clientRaw, func(fd int) error {
		return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 4096)
	}); err != nil {
		t.Fatal(err)
	}

	payload := make([]byte, 4<<10)
	fillSCTPSendQueue(t, accepted, payload)
	if err := accepted.SetWriteDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := accepted.Write(payload); done <- err }()
	activeWrite = done
	timer := time.NewTimer(time.Second)
	select {
	case err := <-done:
		activeWrite = nil
		timer.Stop()
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("blocked Write deadline error = %v", err)
		}
	case <-timer.C:
		_ = accepted.Close()
		select {
		case <-done:
			activeWrite = nil
		case <-time.After(time.Second):
			t.Fatal("write deadline and close did not join blocked SCTP Write")
		}
		t.Fatal("write deadline did not wake blocked SCTP Write")
	}

	if err := accepted.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done = make(chan error, 1)
	go func() {
		close(started)
		_, err := accepted.Write(payload)
		done <- err
	}()
	activeWrite = done
	<-started
	select {
	case err := <-done:
		activeWrite = nil
		t.Fatalf("Write completed despite a full peer receive window: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := accepted.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		activeWrite = nil
		if err == nil {
			t.Fatal("blocked Write succeeded after connection close")
		}
	case <-time.After(time.Second):
		t.Fatal("connection close did not wake blocked SCTP Write")
	}
}

func fillSCTPSendQueue(t *testing.T, conn *sctpConn, payload []byte) {
	t.Helper()
	sent := 0
	lastProgress := time.Now()
	deadline := time.Now().Add(3 * time.Second)
	for sent < 64 && time.Now().Before(deadline) {
		var n int
		var sendErr error
		control := sctpSendInfo(conn.mode)
		err := conn.raw.Control(func(fd uintptr) {
			n, sendErr = unix.SendmsgN(int(fd), payload, control, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		})
		if err != nil {
			t.Fatalf("control SCTP send queue: %v", err)
		}
		if errors.Is(sendErr, unix.EAGAIN) || errors.Is(sendErr, unix.EWOULDBLOCK) {
			if sent > 0 && time.Since(lastProgress) >= 150*time.Millisecond {
				return
			}
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if sendErr != nil {
			t.Fatalf("fill SCTP send queue: %v", sendErr)
		}
		if n != len(payload) {
			t.Fatalf("fill SCTP send queue short write = %d/%d", n, len(payload))
		}
		sent++
		lastProgress = time.Now()
	}
	if sent == 64 {
		t.Fatal("SCTP send queue did not fill within 256 KiB")
	}
	t.Fatal("SCTP send queue did not remain full within 3 seconds")
}

func sctpTestControl(raw syscall.RawConn, control func(int) error) error {
	var controlErr error
	err := raw.Control(func(fd uintptr) { controlErr = control(int(fd)) })
	if err != nil {
		return err
	}
	return controlErr
}
