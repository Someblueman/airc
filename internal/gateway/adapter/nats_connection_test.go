//go:build darwin || linux

package adapter

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSConnectionHeartbeatAndTerminalOrdering(t *testing.T) {
	client, c, r := natsConnectionFixture(t, false)
	if err := client.SetReadDeadline(time.Now().Add(natsHeartbeatInterval + 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if f, err := natswire.Read(r); err != nil || f.Command != "PING" {
		t.Fatalf("scheduled heartbeat: %v", err)
	}
	if time.Since(started) < natsHeartbeatInterval-time.Second {
		t.Fatal("heartbeat interval shortened")
	}
	c.pong()
	terminal := make(chan error, 1)
	go func() {
		terminal <- c.writeBefore(natswire.Frame{Command: "-err", Error: "closed"}, time.Time{}, false)
	}()
	if f, err := natswire.Read(r); err != nil || f.Command != "-ERR" {
		t.Fatal("terminal", err)
	}
	if err := <-terminal; err != nil {
		t.Fatal(err)
	}
	if err := c.writeBefore(natswire.Frame{Command: "PONG"}, time.Time{}, false); !errors.Is(err, natswire.ErrFrame) {
		t.Fatal("write after terminal", err)
	}
	if err := c.ping(); !errors.Is(err, natswire.ErrFrame) {
		t.Fatal("heartbeat after terminal", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := natswire.Read(r); err == nil {
		t.Fatal("frame after terminal")
	}
	c.close()
	select {
	case <-c.heartbeatDone:
	default:
		t.Fatal("heartbeat not joined")
	}
}

func TestNATSOutstandingPingBound(t *testing.T) {
	_, c, r := natsConnectionFixture(t, false)
	for i := 0; i < 3; i++ {
		done := make(chan error, 1)
		go func() { done <- c.ping() }()
		if i < 2 {
			if f, err := natswire.Read(r); err != nil || f.Command != "PING" {
				t.Fatal(err)
			}
		}
		if err := <-done; (err == nil) != (i < 2) {
			t.Fatalf("ping %d: %v", i, err)
		}
	}
	c.pong()
	done := make(chan error, 1)
	go func() { done <- c.ping() }()
	if f, err := natswire.Read(r); err != nil || f.Command != "PING" {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("PONG did not reset bound", err)
	}
}

func TestNATSConnectionCancellationJoinsBlockedIO(t *testing.T) {
	for _, direction := range []string{"read", "write"} {
		t.Run(direction, func(t *testing.T) {
			_, c, _ := natsConnectionFixture(t, false)
			done := make(chan error, 1)
			go func() {
				if direction == "read" {
					_, err := c.readBefore(time.Time{})
					done <- err
				} else {
					done <- c.writeBefore(natswire.Frame{Command: "MSG", Subject: "airc.response", SID: "1", Payload: make([]byte, natswire.MaxPayload)}, time.Time{}, false)
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("IO was not blocked: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			closed := make(chan struct{})
			go func() { c.close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("close did not join")
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("blocked IO succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("blocked IO not joined")
			}
			if c.ctx.Err() == nil {
				t.Fatal("owner context live")
			}
		})
	}
}

func TestNATSOpenFailureClosesSocket(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	if _, err := openNATSConnection(t.Context(), server, &tls.Config{}, nil, nil, natsInfo{}); err == nil {
		t.Fatal("invalid owner opened")
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := peer.Read(b[:]); err == nil {
		t.Fatal("failed socket retained")
	}
}
