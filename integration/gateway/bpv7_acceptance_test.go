//go:build darwin || linux

package gateway_test

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
	"testing"
	"time"
)

func TestBPv7DelayedNativeAcceptanceAndStableReplay(t *testing.T) {
	f := newBPFixture(t)
	now := time.Now().Unix()
	body := "delayed BPv7\nUnicode 🙂 $(touch must-not-execute)"
	// The pre-aged creation checks original validity; physically retain the exact file bytes for two seconds as well.
	p := bpIntent(t, f, 17, now-120, now+300, body)
	wire := p.wire()
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	report := bpCall(t, f, wire, true)
	if len(report.Entries) != 1 {
		t.Fatal(report)
	}
	e := report.Entries[0]
	if !e.Imported || e.ReplayRejected || e.Result.State != core.Accepted || !e.Result.Durable || e.Result.SendAttempts != 1 || e.Result.ExpiresAt != now+300 || e.Principal != f.config.Principals[0].ID || e.Binding != f.config.Principals[0].Binding {
		t.Fatal(e)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == report.Entries[0].Result.MessageID })
	if event.From != "fixture-author" || event.Target != "#gateway-fixture" || event.Message != body {
		t.Fatal(event)
	}
	replay := bpCall(t, f, wire, false)
	if len(replay.Entries) != 1 || replay.Entries[0].Imported || !replay.Entries[0].ReplayRejected || replay.Entries[0].Result.MessageID != e.Result.MessageID || replay.Entries[0].Result.SendAttempts != 1 {
		t.Fatal(replay)
	}
	// Same identity with another signed body is a conflict, never fresh work.
	changed := bpIntent(t, f, 17, now-120, now+300, "changed intent")
	conflict := bpCall(t, f, changed.wire(), false)
	if len(conflict.Entries) != 0 {
		t.Fatal("conflict admitted", conflict)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(native) != 1 || native[0].Message != body {
		t.Fatal(native)
	}
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("workers did not release journal", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}
