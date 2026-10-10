//go:build darwin || linux

package adapter

import (
	"io"
	"net"
	"testing"
	"time"
)

// stalledNativeRead accepts a connection without registering the native session.
// The returned assertion proves the admission deadline closed that real socket.
func stalledNativeRead(t *testing.T, address string) func() {
	t.Helper()
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-finished
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})
	return func() {
		t.Helper()
		select {
		case conn := <-accepted:
			defer conn.Close()
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, conn); err != nil {
				t.Fatal("native read socket retained", err)
			}
		case <-time.After(time.Second):
			t.Fatal("read did not reach native connection")
		}
	}
}
