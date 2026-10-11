package gateway_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendWireDeadlinesAndFeatures(t *testing.T) {
	for _, mode := range []string{"stall", "withhold", "features"} {
		t.Run(mode, func(t *testing.T) {
			socket := fixture(t)
			const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			provision := dial(t, socket, "fixture-author", token, true, true)
			_ = provision.Close()
			observer := dial(t, socket, "fixture-observer", "", false, false)
			if err := observer.Join("#gateway-fixture"); err != nil {
				t.Fatal(err)
			}
			next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
			proxy, done, _, _ := backendProxy(t, socket, mode, nil)
			c, identity := acceptanceJournalFixture(t, proxy, token)
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			s, err := store.Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			expires := time.Now().Unix() + 2
			r := core.Request{Version: 1, RequestID: "boundary", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "bounded attempt", ExpiresAt: &expires}
			if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			o, err := b.Run(ctx, g)
			if err != nil || o.Receipt != nil || time.Since(start) > 3*time.Second {
				t.Fatal("unbounded attempt or false acceptance", o, err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("backend returned with a surviving lease")
			}
			if mode == "withhold" {
				seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.RequestID == o.Attempt.Job.UpstreamID })
				if seen.Message != r.Message || !o.Completion.MayHavePosted {
					t.Fatal("lost receipt lost possible acceptance")
				}
			} else if o.Completion.MayHavePosted {
				t.Fatal("pre-write failure invented a post")
			}
			if mode == "features" && (o.Completion.Failure.Code != core.UnsupportedOperation || o.Completion.Failure.Transient) {
				t.Fatal("feature value 0 was accepted", o)
			}
			j, err := s.Complete(ctx, g, o.Completion, time.Now())
			if err != nil || j.Result.AcceptanceUncertain != (mode == "withhold") {
				t.Fatal("completion lost backend evidence", j, err)
			}
		})
	}
}

func TestBackendVerifiedTLS(t *testing.T) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	for _, host := range []string{"localhost", "wrong.example", "bad-token"} {
		t.Run(host, func(t *testing.T) {
			cert := testtls.New(t)
			proxy, done, _, _ := backendProxy(t, socket, "relay", cert.Server)
			c, identity := acceptanceJournalFixture(t, socket, token)
			c.Upstream.Network, c.Upstream.Address = "tcp", proxy
			c.Upstream.TLSServerName, c.Upstream.TLSCAFile = host, cert.CAPath
			if host == "bad-token" {
				c.Upstream.TLSServerName = "localhost"
			}
			c.Upstream.AccessTokenFile = filepath.Join(filepath.Dir(c.StateDir), "access.token")
			access := strings.Repeat("b", 64)
			if host == "bad-token" {
				access = strings.Repeat("c", 64)
			}
			if err := os.WriteFile(c.Upstream.AccessTokenFile, []byte(access), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			s, err := store.Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := core.Request{Version: 1, RequestID: "tls", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "verified TLS backend"}
			if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			o, err := b.Run(ctx, g)
			if err != nil {
				t.Fatal(err)
			}
			if host == "localhost" && (o.Receipt == nil || o.Receipt.Message != r.Message) {
				t.Fatal("verified TLS failed", o)
			}
			if host != "localhost" && (o.Receipt != nil || o.Completion.MayHavePosted || o.Completion.Failure.Code != core.AuthenticationFailed || o.Completion.Failure.Transient) {
				t.Fatal("wrong TLS host was accepted", o)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("TLS lease survived Run")
			}
		})
	}
}
