//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

func TestIRCOwnerWholeDurableReplayRefusalAndJoinedCancellation(t *testing.T) {
	f := newBridgeAdapterFixture(t)
	processed := make(chan struct{})
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	server := ircOwnerFixture(t, f, func(conn *tls.Conn) {
		for _, line := range []string{
			"@msgid=bad;time=" + stamp.Format("2006-01-02T15:04:05.000Z") + " :unknown!u@h PRIVMSG #external :refused",
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
	job, err := f.guestJob(t, "irc-alice", id, time.Until(deadline))
	if err != nil || !job.Result.Durable || job.Request.Message != "whole 世界 body  " || *job.Request.Target != f.target {
		t.Fatal("whole original intent", job, err)
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
	if _, err := f.guestJob(t, "irc-unknown", ircOwnerRequestID("bad"), 0); err == nil {
		t.Fatal("event without a services account admitted")
	}
}

func ircOwnerRequestID(id string) string {
	return ircOwnerRequestIDParts("airc-irc-event-v1", "network", id)
}

func ircOwnerRequestIDParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
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
	// The silence outlasts the setup budget; a short one keeps it brief.
	budget := auth.IRCSetupBudget
	t.Cleanup(func() { auth.IRCSetupBudget = budget })
	auth.IRCSetupBudget = time.Second
	f := newBridgeAdapterFixture(t)
	server := ircOwnerFixture(t, f, func(conn *tls.Conn) {
		time.Sleep(auth.IRCSetupBudget + 500*time.Millisecond)
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
	job, err := f.guestJob(t, "irc-alice", ircOwnerRequestID("after-idle"), 6*time.Second)
	if err != nil || !job.Result.Durable || job.Request.Message != "after idle" {
		t.Fatal("event after idle period lost", job, err)
	}
}

// A read request is answered to the requester alone with plain NOTICE lines
// whose parts join into the result JSON; nothing is posted.
func TestIRCOwnerAnswersReadRequestsWithPlainNotices(t *testing.T) {
	f := newBridgeAdapterFixture(t)
	replies := make(chan []BridgeResult, 1)
	server := ircOwnerFixture(t, f, func(conn *tls.Conn) {
		stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		for _, line := range []string{
			"@account=alice;msgid=read-1;time=" + stamp + ` :alice!u@h PRIVMSG #external :AIRC-READ/1 {"operation":"history.read","correlation_id":"c1"}`,
			"@account=alice;msgid=read-2;time=" + stamp + ` :alice!u@h PRIVMSG #external :AIRC-READ/1 {"operation":`,
		} {
			if _, err := io.WriteString(conn, line+"\r\n"); err != nil {
				t.Error(err)
				return
			}
		}
		reader := ircwire.NewReader(conn)
		var results []BridgeResult
		parts := map[string][]string{}
		for len(results) < 2 {
			c, err := reader.Read()
			if err != nil {
				t.Error(err)
				return
			}
			if c.Name != "NOTICE" || len(c.Params) != 1 || c.Params[0] != "alice" {
				t.Error("reply not addressed to the requester", c)
				return
			}
			id, index, count, part, ok := parseIRCResponseLine(c.Trailing)
			if !ok || index != len(parts[id]) {
				t.Error("bad result line", c.Trailing)
				return
			}
			parts[id] = append(parts[id], part)
			if len(parts[id]) == count {
				var r BridgeResult
				if err := json.Unmarshal([]byte(strings.Join(parts[id], "")), &r); err != nil {
					t.Error(err)
					return
				}
				results = append(results, r)
			}
		}
		replies <- results
	})
	ctx, cancel := context.WithCancel(t.Context())
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- ServeIRC(ctx, server, f.d, slots, requests) }()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case results := <-replies:
		unrouted, malformed := results[0], results[1]
		if unrouted.Error == nil || unrouted.Error.Code != core.UnsupportedOperation || unrouted.CorrelationID != "c1" || unrouted.Author != "irc-alice" || unrouted.Room != "#external" || unrouted.Target != f.target || unrouted.RequestID != ircOwnerRequestID("read-1") {
			t.Fatal("unrouted read reply", unrouted, unrouted.Error)
		}
		if malformed.Error == nil || malformed.Error.Code != core.InvalidRequest {
			t.Fatal("malformed read reply", malformed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no read replies")
	}
	if _, err := f.guestJob(t, "irc-alice", ircOwnerRequestID("read-1"), 0); err == nil {
		t.Fatal("read request was posted")
	}
}

// parseIRCResponseLine splits one NOTICE body of ircwire.PlainResponseLines.
func parseIRCResponseLine(body string) (id string, index, count int, part string, ok bool) {
	rest, ok := strings.CutPrefix(body, ircwire.ResponseMarker+" ")
	fields := strings.SplitN(rest, " ", 3)
	if !ok || len(fields) != 3 {
		return "", 0, 0, "", false
	}
	_, err := fmt.Sscanf(fields[1], "%d/%d", &index, &count)
	return fields[0], index, count, fields[2], err == nil && count > 0
}
