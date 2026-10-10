//go:build linux && arm64

package adapter

import (
	"net"
	"syscall"
	"testing"
	"time"
)

type sctpInterruptOnceRawConn struct {
	syscall.RawConn
	interrupted bool
}

func (c *sctpInterruptOnceRawConn) Read(fn func(uintptr) bool) error {
	if !c.interrupted {
		c.interrupted = true
		return syscall.EINTR
	}
	return c.RawConn.Read(fn)
}

func TestSCTPListenerAcceptRetriesInterruptedPoll(t *testing.T) {
	listener, err := ListenSCTP("127.0.0.1:0", SCTPOrderedTTL)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sctpListener := listener.(*sctpListener)
	sctpListener.raw = &sctpInterruptOnceRawConn{RawConn: sctpListener.raw}

	accepted := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := listener.Accept()
		accepted <- struct {
			conn net.Conn
			err  error
		}{conn, err}
	}()
	client := sctpTestClientWithTTL(t, listener.Addr(), 0, 512)
	defer client.close()
	select {
	case result := <-accepted:
		if result.err != nil {
			t.Fatalf("Accept after EINTR: %v", result.err)
		}
		defer result.conn.Close()
		if err := result.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		want := []byte("accepted-after-eintr")
		client.sendTTL(t, want)
		buffer := make([]byte, sctpRecordLimit)
		if n, err := result.conn.Read(buffer); err != nil || string(buffer[:n]) != string(want) {
			t.Fatalf("record after interrupted Accept = %q, %v", buffer[:n], err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Accept did not recover after one EINTR")
	}
}
