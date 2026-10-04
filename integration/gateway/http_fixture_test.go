package gateway_test

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

type httpFixture struct {
	server       *httptest.Server
	journal      *store.Store
	token, other string
	identity     auth.Identity
	observer     *irc.Client
}

func gatewayHTTPFixture(t *testing.T, http2 bool) httpFixture {
	t.Helper()
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, identity := acceptanceJournalFixture(t, socket, account)
	other := c.Principals[0]
	other.ID = "other"
	other.BearerTokenFile = filepath.Join(filepath.Dir(other.BearerTokenFile), "other.token")
	if err := auth.CreateCredential(other.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, other)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("HTTP worker failed to join")
		}
	})
	d, err := dispatch.New(s, v, []core.Operation{core.Send, core.Reply, core.ReceiptGet})
	if err != nil {
		t.Fatal(err)
	}
	server, err := adapter.HTTPServer(d, 8)
	if err != nil {
		t.Fatal(err)
	}
	tls := httptest.NewUnstartedServer(server.Handler)
	tls.Config = server
	tls.TLS = server.TLSConfig.Clone()
	tls.EnableHTTP2 = http2
	tls.StartTLS()
	t.Cleanup(tls.Close)
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := auth.ReadToken(other.BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	return httpFixture{tls, s, token, otherToken, identity, observer}
}

func httpCall(t *testing.T, f httpFixture, method, path, token string, body []byte) (int, int, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.TLS == nil || response.TLS.Version < cryptotls.VersionTLS13 {
		t.Fatal("HTTP ingress did not negotiate TLS 1.3")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated response cacheable")
	}
	return response.StatusCode, response.ProtoMajor, data
}

func requestJSON(t *testing.T, r core.Request) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
