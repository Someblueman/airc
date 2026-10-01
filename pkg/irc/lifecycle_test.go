package irc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCloseInterruptsReconnectRegistration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	reconnecting := make(chan struct{})
	peerClosed := make(chan struct{})
	go func() {
		defer close(peerClosed)
		for attempt := 0; attempt < 2; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			scanner := bufio.NewScanner(conn)
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "USER ") {
					break
				}
			}
			if attempt == 0 {
				fmt.Fprint(conn, ":server 001 me :Welcome\r\n")
				conn.Close()
			} else {
				close(reconnecting)
				for scanner.Scan() {
				}
				conn.Close()
			}
		}
	}()
	client, err := Dial(Config{Nick: "me", Addr: listener.Addr().String(), Reconnect: true, MinBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-reconnecting:
	case <-time.After(time.Second):
		client.Close()
		t.Fatal("client did not reconnect")
	}
	start := time.Now()
	client.Close()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("Close waited for registration timeout")
	}
	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("reconnect socket remained open")
	}
}

type trackedWrite struct {
	net.Conn
	started chan struct{}
}

func (c *trackedWrite) Write(data []byte) (int, error) {
	close(c.started)
	return c.Conn.Write(data)
}

func TestCloseInterruptsBlockedWriteWithoutWaitingForWriteMutex(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	conn := &trackedWrite{Conn: local, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{cfg: Config{WriteTimeout: time.Hour}, conn: conn, events: make(chan Event, 1), done: make(chan struct{}), finished: make(chan struct{}), stop: cancel}
	go client.run(ctx, conn, newScanner(conn))
	written := make(chan error, 1)
	go func() { written <- client.writeLine("PING :blocked\r\n") }()
	<-conn.started
	start := time.Now()
	client.Close()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("Close waited behind socket I/O")
	}
	select {
	case err := <-written:
		if err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("writer leaked")
	}
}
