//go:build unix

package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

func TestIRCEventProofRequiresVerifiedSessionAndExactGrant(t *testing.T) {
	server, b, _ := ircSessionFixture(t, "events")
	b.Routes[0].Account = "other"
	b.Routes[0].Target.Name = "#other"
	session, err := server.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for range 2 {
		event, err := session.ReadEvent()
		if !errors.Is(err, ErrAuthentication) || event.Valid() || event.RequestID() != "" {
			t.Fatal("loop/client tag authority", event.Valid(), err)
		}
	}
	event, err := session.ReadEvent()
	if err != nil || !event.Valid() || event.Identity().ID() != "device" || event.Identity().Binding() != "fixture-backend" || event.Target().Name != "#Allowed" || event.Body() != " exact 世界  " || event.Account() != "alice" || event.Room() != "#external" || len(event.RequestID()) != 64 || len(event.Fingerprint()) != 64 {
		t.Fatal("mapped whole event", err)
	}
	replay, err := session.ReadEvent()
	if err != nil || replay.RequestID() != event.RequestID() || replay.Fingerprint() != event.Fingerprint() || replay.Nickname() == event.Nickname() || !replay.ReplayDeadline().Equal(event.ReplayDeadline()) {
		t.Fatal("replayed identity", err)
	}
	ops := event.Operations()
	ops[0] = core.Watch
	target := event.Target()
	target.Name = "#other"
	if event.Operations()[0] != core.Send || event.Target().Name != "#Allowed" {
		t.Fatal("mutable authority alias")
	}
	if _, err = session.ReadEvent(); err != ircwire.ErrRegistration || session.registration.Ready() {
		t.Fatal("removed capability retained", err)
	}
	var empty VerifiedIRCEvent
	if empty.Valid() || empty.RequestID() != "" || empty.Fingerprint() != "" {
		t.Fatal("zero proof authority")
	}
	var nilSession *IRCSession
	if event, err := nilSession.ReadEvent(); err != ErrAuthentication || event.Valid() {
		t.Fatal("nil session proof")
	}
}

func TestIRCEventFreshnessAndAuthorityRefusals(t *testing.T) {
	server, _, _ := ircSessionFixture(t, "valid")
	now := time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	valid := "@account=alice;msgid=stable;time=2026-10-08T05:59:59.000Z :alice!user@host PRIVMSG #external :whole 世界"
	cases := []string{
		strings.Replace(valid, "account=alice", "+account=alice", 1),
		strings.Replace(valid, "account=alice", "account=ALICE", 1),
		strings.Replace(valid, "account=alice", "account=bridge-account", 1),
		strings.Replace(valid, "msgid=stable", "msgid=", 1),
		strings.Replace(valid, "msgid=stable", "msgid=:bad", 1),
		strings.Replace(valid, "msgid=stable", "msgid=bad\\sID", 1),
		strings.Replace(valid, "05:59:59.000Z", "06:00:00.001Z", 1),
		strings.Replace(valid, "05:59:59.000Z", "05:59:00.000Z", 1),
		strings.Replace(valid, "05:59:59.000Z", "05:59:59.0000Z", 1),
		strings.Replace(valid, "#external", "#other", 1),
		strings.Replace(valid, "#external", "#external,#other", 1),
		strings.Replace(valid, "alice!user@host", "server", 1),
		strings.Replace(valid, "alice!user@host", "alice!@host", 1),
		strings.Replace(valid, "whole 世界", "\x01ACTION text\x01", 1),
		strings.Replace(valid, "whole 世界", "   ", 1),
	}
	for _, line := range cases {
		command, err := ircwire.Parse([]byte(line + "\r\n"))
		if err != nil {
			t.Fatal("fixture", err)
		}
		event, err := server.verifyEvent(command, now)
		if err != ErrAuthentication || event.Valid() {
			t.Fatal("invalid event authenticated", line, err)
		}
	}
	command, err := ircwire.Parse([]byte(valid + "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	event, err := server.verifyEvent(command, now)
	if err != nil {
		t.Fatal(err)
	}
	if !event.ReplayDeadline().Equal(event.Occurred().Add(time.Minute)) {
		t.Fatal("renewed freshness")
	}
	command.Trailing = "changed"
	changed, err := server.verifyEvent(command, now)
	if err != nil || changed.RequestID() != event.RequestID() || changed.Fingerprint() == event.Fingerprint() {
		t.Fatal("content conflict identity", err)
	}
	server.network = "other-network"
	scoped, err := server.verifyEvent(command, now)
	if err != nil || scoped.RequestID() == event.RequestID() {
		t.Fatal("cross-network ID collision", err)
	}
}
