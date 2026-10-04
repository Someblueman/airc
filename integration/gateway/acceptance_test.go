package gateway_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWireReceiptBecomesDurableAcceptance(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const accountToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", accountToken, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", accountToken, false, true)
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "wire-fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	accountFile := filepath.Join(root, "account.token")
	if err := os.WriteFile(accountFile, []byte(accountToken), 0600); err != nil {
		t.Fatal(err)
	}
	bearerFile := filepath.Join(root, "bearer.token")
	if err := auth.CreateCredential(bearerFile); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "fixture-backend", Network: "unix", Address: socket, Account: "fixture-author", AccountTokenFile: accountFile}}
	c.Principals = []config.Principal{{ID: "device", Binding: "fixture-backend", BearerTokenFile: bearerFile, Operations: []core.Operation{core.Send, core.Reply, core.ReceiptGet}, Targets: []core.Target{{Kind: "room", Name: "#gateway-fixture"}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(bearerFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := core.Request{Version: 1, RequestID: "logical-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "journal to wire\r\nindependent observer"}
	if _, _, err := s.Admit(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := author.SendWithID(a.Job.Request.Target.Name, a.Job.Request.Message, a.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == a.Job.UpstreamID })
	observedAt := time.Now()
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if seen.From != "fixture-author" || seen.Target != r.Target.Name || seen.Message != a.Job.Request.Message {
		t.Fatal("observer disagrees with admitted operation")
	}
	j, err := s.RecordAcceptance(ctx, g, receipt, observedAt, time.Now())
	if err != nil || j.Result.State != core.Accepted || j.Result.Persisted == nil || !*j.Result.Persisted {
		t.Fatal("wire receipt was not saved", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	j, new, err := s.Admit(ctx, identity, r, 0, time.Now())
	if err != nil || new || j.Receipt == nil || j.Receipt.ID != receipt.ID {
		t.Fatal("lost response/reopen failed to replay acceptance", err)
	}
	if _, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now()); !errors.Is(err, store.ErrNotReady) {
		t.Fatal("saved real acceptance allowed another send", err)
	}
	reply := core.Request{Version: 1, RequestID: "logical-reply", Operation: core.Reply, Target: r.Target, Message: "reply through journal", ParentID: receipt.ID}
	if _, _, err := s.Admit(ctx, identity, reply, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	rg, err := s.Reserve(ctx, identity.ID(), reply.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ra, err := rg.Consume(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(ctx, identity.ID()); err != nil {
		t.Fatal(err)
	}
	if err := author.ReplyWithID(reply.ParentID, ra.Job.Request.Message, ra.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	rr := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == ra.Job.UpstreamID })
	observedReplyAt := time.Now()
	ro := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == rr.ID })
	if ro.ReplyTo != receipt.ID || ro.ThreadID != receipt.ID {
		t.Fatal("observer reply linkage lost")
	}
	rj, err := s.RecordAcceptance(ctx, rg, rr, observedReplyAt, time.Now())
	if err != nil || rj.Receipt.ReplyTo != receipt.ID || rj.Receipt.ThreadID != receipt.ID {
		t.Fatal("pre-revocation reply acceptance lost", err)
	}
}
