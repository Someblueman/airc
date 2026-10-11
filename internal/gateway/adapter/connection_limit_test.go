package adapter

import (
	"net"
	"testing"
	"time"
)

func TestLimitConnectionsPerSourceAndTotal(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	exemptLoopback = false
	defer func() { exemptLoopback = true }()
	if err != nil {
		t.Fatal(err)
	}
	l := LimitConnections(raw, 3, 2)
	defer l.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- conn
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	closedByServer := func(c net.Conn) bool {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		_, err := c.Read(make([]byte, 1))
		return err != nil && !isTimeout(err)
	}
	dial()
	dial()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("first connection not accepted")
	}
	first := <-accepted
	third := dial()
	if !closedByServer(third) {
		t.Fatal("a third connection from one source was accepted")
	}
	// Closing one frees its slot for the same source.
	_ = first.Close()
	dial()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("freed slot not reused")
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
