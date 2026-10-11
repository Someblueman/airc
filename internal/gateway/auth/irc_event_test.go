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

func TestIRCEventAuthorComesFromTheServerAccount(t *testing.T) {
	server, _, _ := ircSessionFixture(t, "events")
	session, err := server.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for range 2 { // the bridge's own echo, and a client-only +account tag
		event, err := session.ReadEvent()
		if !errors.Is(err, ErrAuthentication) || event.Valid() || event.RequestID() != "" {
			t.Fatal("loop/client tag authority", event.Valid(), err)
		}
	}
	event, err := session.ReadEvent()
	e := event.Event()
	r := e.Request()
	if err != nil || !event.Valid() || e.Author().Author != (core.Author{Nick: "irc-alice"}) || e.Author().Source != "bridge:network:irc-alice" ||
		r.Operation != core.Send || r.Message != " exact 世界  " || r.Target.Name != "#Allowed" || event.Account() != "alice" || event.Room() != "#external" || len(event.RequestID()) != 64 {
		t.Fatal("guest event", err, e.Author(), r)
	}
	// The IRC nickname is not identity: a renamed sender with the same
	// account and message ID is the same author and the same request.
	replay, err := session.ReadEvent()
	if err != nil || replay.RequestID() != event.RequestID() || replay.Event().Author().Author != e.Author().Author || replay.Nickname() == event.Nickname() || !replay.ReplayDeadline().Equal(event.ReplayDeadline()) {
		t.Fatal("replayed identity", err)
	}
	// A sender the server does not vouch for has no author at all.
	if anonymous, err := session.ReadEvent(); !errors.Is(err, ErrIRCEvent) || anonymous.Valid() {
		t.Fatal("unidentified sender admitted", err)
	}
	mapped, err := session.ReadEvent()
	if a := mapped.Event().Author(); err != nil || a.Author != (core.Author{Nick: "carol", Registered: true}) || a.Credential.Empty() || mapped.Nickname() != "someone" {
		t.Fatal("mapped account", err, a)
	}
	read, err := session.ReadEvent()
	if r := read.Event().Request(); err != nil || !read.Event().IsRead() || !read.Event().Allowed() || r.Operation != core.HistoryRead || r.Limit != 3 {
		t.Fatal("read request", err, r)
	}
	if _, err = session.ReadEvent(); err != ircwire.ErrRegistration || session.registration.Ready() {
		t.Fatal("removed capability retained", err)
	}
	var empty VerifiedIRCEvent
	if empty.Valid() || empty.RequestID() != "" {
		t.Fatal("zero proof authority")
	}
	var nilSession *IRCSession
	if event, err := nilSession.ReadEvent(); err != ErrAuthentication || event.Valid() {
		t.Fatal("nil session proof")
	}
}

func TestIRCEventFreshnessAndShapeRefusals(t *testing.T) {
	server, _, _ := ircSessionFixture(t, "valid")
	now := time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	valid := "@account=alice;msgid=stable;time=2026-10-08T05:59:59.000Z :alice!user@host PRIVMSG #external :whole 世界"
	cases := []string{
		strings.Replace(valid, "account=alice", "+account=alice", 1),
		strings.Replace(valid, "account=alice;", "", 1),
		strings.Replace(valid, "account=alice", "account=bridge-account", 1),
		strings.Replace(valid, "account=alice", "account=BRIDGE-ACCOUNT", 1),
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
	if err != nil || changed.RequestID() != event.RequestID() || changed.Event().Request().Message != "changed" {
		t.Fatal("content conflict identity", err)
	}
	server.network = "other-network"
	scoped, err := server.verifyEvent(command, now)
	if err != nil || scoped.RequestID() == event.RequestID() {
		t.Fatal("cross-network ID collision", err)
	}
	// Without guests only mapped accounts post.
	server.authors.guests = false
	if _, err := server.verifyEvent(command, now); err != ErrAuthentication {
		t.Fatal("guest admitted with guests off", err)
	}
	command.Tags["account"] = "carol"
	if e, err := server.verifyEvent(command, now); err != nil || !e.Event().Author().Author.Registered {
		t.Fatal("mapped account refused", err)
	}
}
