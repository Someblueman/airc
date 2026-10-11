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
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

type httpFixture struct {
	stateDir                string
	socket                  string
	server                  *httptest.Server
	journal                 *store.Store
	token, other            string
	identity                store.Submission
	observer                *irc.Client
	watchReady, watchJoined <-chan struct{}
	credentials             *detachedCredentials
}

func gatewayHTTPFixture(t *testing.T, http2 bool, proxyMode ...string) httpFixture {
	return gatewayHTTPOriginFixture(t, http2, nil, false, proxyMode...)
}

func gatewayHTTPOriginFixture(t *testing.T, http2 bool, origins []string, plaintext bool, proxyMode ...string) httpFixture {
	return httpGatewayFixture(t, http2, origins, plaintext, false, proxyMode...)
}

func httpGatewayFixture(t *testing.T, http2 bool, origins []string, plaintext, detached bool, proxyMode ...string) httpFixture {
	t.Helper()
	socket := fixture(t)
	var observer *irc.Client
	if plaintext {
		// The interactive browser probe may spend time opening its owned tab.
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		var err error
		observer, err = irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: "fixture-observer", ReadTimeout: 120 * time.Second, WriteTimeout: time.Second})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = observer.Close() })
	} else {
		observer = dial(t, socket, "fixture-observer", "", false, false)
	}
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	backendAddress := socket
	var ready, joined <-chan struct{}
	if len(proxyMode) > 0 {
		backendAddress, joined, ready, _ = backendProxy(t, socket, proxyMode[0], nil)
	}
	c, identity := acceptanceJournalFixture(t, backendAddress, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	var credentials *detachedCredentials
	if detached {
		credentials = provisionDetached(t, &c)
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
	d, err := dispatch.ForListener(s, b, cursors, config.Listener{ID: "http", Protocol: "http", Operations: []core.Operation{core.Send, core.Reply, core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.Directory, core.AgentsRead}, Limits: fixtureListenerLimits()})
	if err != nil {
		t.Fatal(err)
	}
	server, err := adapter.HTTPServer(d, 8, origins)
	if err != nil {
		t.Fatal(err)
	}
	tls := httptest.NewUnstartedServer(server.Handler)
	tls.Config = server
	tls.TLS = server.TLSConfig.Clone()
	tls.EnableHTTP2 = http2
	if plaintext {
		tls.Start()
	} else {
		tls.StartTLS()
	}
	t.Cleanup(tls.Close)
	return httpFixture{stateDir: c.StateDir, socket: socket, server: tls, journal: s, token: fixtureAuthor + ":" + account, other: "fixture-other", identity: identity, observer: observer, watchReady: ready, watchJoined: joined, credentials: credentials}
}

// httpCall sends one request as author, "nick" (a guest) or "nick:token" (a
// registered author), named in the AIRC-Nick and AIRC-Token headers.
func httpCall(t *testing.T, f httpFixture, method, path, author string, body []byte) (int, int, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(r, author)
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

// setAuthorHeaders names author ("nick" or "nick:token") in r's headers.
func setAuthorHeaders(r *http.Request, author string) {
	if author == "" {
		return
	}
	nick, token, _ := strings.Cut(author, ":")
	r.Header.Set("AIRC-Nick", nick)
	if token != "" {
		r.Header.Set("AIRC-Token", token)
	}
}

// postAuthorRequest posts r as the fixture author's plain contract request to
// POST /v1/request, the route that takes any operation.
func postAuthorRequest(t *testing.T, f httpFixture, r core.Request) (int, dispatch.DetachedResult) {
	t.Helper()
	status, _, data := httpCall(t, f, http.MethodPost, "/v1/request", "", []byte(authorRequest(t, r, fixtureAuthor, fixtureAccount)))
	var result dispatch.DetachedResult
	if status == http.StatusOK || status == http.StatusAccepted {
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
	}
	return status, result
}
