//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/tls"
	"io"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

func TestIRCOwnerCapacityLossAndForeignSlotsPreserved(t *testing.T) {
	f := newBridgeAdapterFixture(t)
	processed := make(chan struct{})
	server := ircOwnerFixture(t, f, func(conn *tls.Conn) {
		stamp := time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")
		if _, err := io.WriteString(conn, "@account=alice;msgid=capacity;time="+stamp+" :alice!u@h PRIVMSG #external :not admitted\r\nPING :after-capacity\r\n"); err != nil {
			t.Error(err)
			return
		}
		command, err := ircwire.NewReader(conn).Read()
		if err != nil || command.Name != "PONG" {
			t.Error("capacity boundary", command, err)
			return
		}
		close(processed)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots, requests := make(chan struct{}, 2), make(chan struct{}, 1)
	slots <- struct{}{}
	requests <- struct{}{}
	done := make(chan error, 1)
	go func() { done <- ServeIRC(ctx, server, f.d, slots, requests) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("owner not joined")
			}
		}
	})
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("capacity refusal not processed")
	}
	if _, err := f.guestJob(t, "irc-alice", ircOwnerRequestID("capacity"), 0); err == nil {
		t.Fatal("request cap bypassed")
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation not joined")
	}
	if len(slots) != 1 || len(requests) != 1 {
		t.Fatal("foreign capacity consumed", len(slots), len(requests))
	}
}
