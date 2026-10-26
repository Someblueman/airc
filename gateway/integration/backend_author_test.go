package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// Per-job authors post as themselves against a real AIRC: a guest as an AIRC
// guest under its own nickname, a registered author after AIRC's own login.
// AIRC, not the gateway, refuses a guest on a registered nickname and a bad
// token, and those refusals are permanent and distinct.
func TestBackendPostsAsJobAuthor(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, token)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	run := func(sub store.Submission, id string) (backend.Outcome, store.Job) {
		t.Helper()
		r := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "posted by " + sub.Author.Nick}
		j, _, err := s.AdmitAuthor(ctx, sub, r, 0, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		g, err := s.Reserve(ctx, j.Scope, id, store.SendAttempt, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		o, err := b.Run(ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		if o.Receipt != nil {
			j, err = s.RecordAcceptance(ctx, g, o.Receipt, o.ObservedAt, time.Now())
		} else {
			j, err = s.Complete(ctx, g, o.Completion, time.Now())
		}
		if err != nil {
			t.Fatal(err)
		}
		return o, j
	}
	credential := func(token string) core.Credential {
		c, err := core.NewCredential(token)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	o, j := run(store.Submission{Author: core.Author{Nick: "Guest-Writer"}, Listener: "http"}, "guest-post")
	if o.Receipt == nil || j.Result.State != core.Accepted {
		t.Fatal("guest post failed", o.Completion, j.Result)
	}
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID })
	if !strings.EqualFold(seen.From, "guest-writer") || seen.Message != "posted by Guest-Writer" {
		t.Fatal("guest post not authored by the guest", seen.From)
	}

	_, j = run(store.Submission{Author: core.Author{Nick: "fixture-author"}, Listener: "http"}, "guest-on-registered")
	if j.Result.State != core.Blocked || j.Result.Failure == nil || j.Result.Failure.Code != core.NickRegistered || j.Result.Failure.Transient || j.Result.Failure.UpstreamCode != 498 {
		t.Fatal("guest on a registered nickname not refused as nick_registered", j.Result, j.Result.Failure)
	}

	o, j = run(store.Submission{Author: core.Author{Nick: "fixture-author", Registered: true}, Credential: credential(token), Listener: "ws"}, "registered-post")
	if o.Receipt == nil || j.Result.State != core.Accepted {
		t.Fatal("registered post failed", o.Completion, j.Result)
	}
	seen = next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID })
	if seen.From != "fixture-author" {
		t.Fatal("registered post author", seen.From)
	}

	_, j = run(store.Submission{Author: core.Author{Nick: "fixture-author", Registered: true}, Credential: credential(strings.Repeat("f", 64)), Listener: "ws"}, "bad-token")
	if j.Result.State != core.Blocked || j.Result.Failure == nil || j.Result.Failure.Code != core.AuthenticationFailed || j.Result.Failure.Transient {
		t.Fatal("bad token not refused as authentication_failed", j.Result, j.Result.Failure)
	}
}
