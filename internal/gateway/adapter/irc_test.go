//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

func TestIRCOwnerWholeDurableReplayRefusalAndJoinedCancellation(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	processed := make(chan struct{})
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	server := ircOwnerFixture(t, f, func(conn *tls.Conn) {
		for _, line := range []string{
			"@account=unknown;msgid=bad;time=" + stamp.Format("2006-01-02T15:04:05.000Z") + " :unknown!u@h PRIVMSG #external :refused",
			"@account=alice;msgid=stable;time=" + stamp.Format("2006-01-02T15:04:05.000Z") + " :alice!u@h PRIVMSG #external :whole 世界 body  ",
			"@account=alice;msgid=stable;time=" + stamp.Format("2006-01-02T15:04:05.000Z") + " :newnick!u@h PRIVMSG #external :whole 世界 body  ",
		} {
			if _, err := io.WriteString(conn, line+"\r\n"); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := io.WriteString(conn, "PING :after-replay\r\n"); err != nil {
			t.Error(err)
			return
		}
		pong, err := ircwire.NewReader(conn).Read()
		if err != nil || pong.Name != "PONG" {
			t.Error("post-replay control", pong, err)
			return
		}
		close(processed)
	})
	ctx, cancel := context.WithCancel(t.Context())
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- ServeIRC(ctx, server, f.d, slots, requests) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("IRC owner not joined")
		}
	})
	id := ircOwnerRequestID("stable")
	deadline := time.Now().Add(time.Second)
	for {
		job, err := f.s.Lookup(t.Context(), f.identity, id)
		if err == nil {
			if !job.Result.Durable || job.Request.Message != "whole 世界 body  " || *job.Request.Target != f.c.Principals[0].Targets[0] {
				t.Fatal("whole original intent", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("IRC send not admitted", err)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("replay processing not reached")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(time.Second):
		t.Fatal("owner cancellation not joined")
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("capacity leaked")
	}
	if _, err := f.s.Lookup(t.Context(), f.identity, ircOwnerRequestID("bad")); err == nil {
		t.Fatal("unmapped event admitted")
	}
}

func ircOwnerRequestID(id string) string {
	h := sha256.New()
	for _, part := range []string{"airc-irc-event-v1", "network", id} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestIRCOwnerRejectsMissingCapacity(t *testing.T) {
	if err := ServeIRC(t.Context(), &auth.IRCServer{}, nil, make(chan struct{}, 1), make(chan struct{}, 1)); err == nil {
		t.Fatal("missing dispatcher")
	}
}

// Real servers stay silent while a channel is quiet. The owner must keep that
// session (no reconnect gap) and still join promptly on cancellation.
func TestIRCOwnerIdleSessionStaysConnected(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	server := ircOwnerFixture(t, f, func(conn *tls.Conn) {
		time.Sleep(3500 * time.Millisecond)
		stamp := time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")
		if _, err := io.WriteString(conn, "@account=alice;msgid=after-idle;time="+stamp+" :alice!u@h PRIVMSG #external :after idle\r\n"); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- ServeIRC(ctx, server, f.d, slots, requests) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("idle IRC owner not joined")
		}
	}()
	deadline := time.Now().Add(6 * time.Second)
	for {
		job, err := f.s.Lookup(t.Context(), f.identity, ircOwnerRequestID("after-idle"))
		if err == nil {
			if !job.Result.Durable || job.Request.Message != "after idle" {
				t.Fatal("event after idle", job)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("event after idle period lost", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
