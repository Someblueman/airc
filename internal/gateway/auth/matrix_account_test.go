//go:build unix

package auth

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func matrixHTTPFixture(t *testing.T, handler http.HandlerFunc) *MatrixServer {
	t.Helper()
	cert := testtls.New(t)
	peer := httptest.NewUnstartedServer(handler)
	peer.TLS = cert.Server.Clone()
	peer.StartTLS()
	t.Cleanup(peer.Close)
	c, carol := bridgeFixtureConfig(t)
	token := filepath.Join(privateTemp(t), "token")
	if err := os.WriteFile(token, []byte("matrix-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.MatrixBridge{ID: "matrix", Address: peer.Listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Account: "@bridge:localhost", AccessTokenFile: token, FreshnessSeconds: 60,
		Accounts: []config.BridgeAccount{{External: "@carol:localhost", Nick: "carol", AccountTokenFile: carol}},
		Routes:   []config.MatrixRoute{{Room: "!room", Target: core.Target{Kind: "room", Name: "#Allowed"}, Operations: []core.Operation{core.Send, core.HistoryRead}}}}
	s, err := NewMatrixServer(c, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMatrixVerifiedAccountHTTPBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "wrong", "guest", "duplicate", "oversize", "redirect", "denied", "encoding"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/_matrix/client/v3/account/whoami" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer matrix-secret" || r.TLS.ServerName != "localhost" {
					t.Error("wrong authenticated boundary")
				}
				w.Header().Set("Content-Type", "application/json")
				body := `{"user_id":"@bridge:localhost","device_id":"AIRC","is_guest":false}`
				switch mode {
				case "wrong":
					body = `{"user_id":"@other:localhost"}`
				case "guest":
					body = `{"user_id":"@bridge:localhost","is_guest":true}`
				case "duplicate":
					body = `{"user_id":"@other:localhost","user_id":"@bridge:localhost"}`
				case "oversize":
					body = strings.Repeat(" ", 16385) + body
				case "redirect":
					w.Header().Set("Location", "https://localhost/elsewhere")
					w.WriteHeader(302)
				case "denied":
					w.WriteHeader(401)
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				}
				_, _ = io.WriteString(w, body)
			})
			err := s.VerifyAccount(context.Background())
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if calls != 1 {
				t.Fatal("redirect/retry", calls)
			}
			if s.security.MinVersion != tls.VersionTLS13 {
				t.Fatal("TLS floor")
			}
		})
	}
}

func TestMatrixAccountCancellationJoinsRequest(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan struct{})
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(done) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.VerifyAccount(ctx) }()
	<-entered
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancel accepted")
	}
	<-done
}

func TestMatrixAccountTLSRefusalAndClosedCredentials(t *testing.T) {
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS reached HTTP") })
	s.security.RootCAs = testtls.New(t).Client.RootCAs
	if s.VerifyAccount(context.Background()) == nil {
		t.Fatal("untrusted certificate accepted")
	}
	s.Close()
	if s.VerifyAccount(context.Background()) == nil {
		t.Fatal("closed token accepted")
	}
	var missing context.Context
	if s.VerifyAccount(missing) == nil {
		t.Fatal("nil context accepted")
	}
}

func TestMatrixAccountAbsoluteDeadline(t *testing.T) {
	done := make(chan struct{})
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); close(done) })
	start := time.Now()
	if s.VerifyAccount(context.Background()) == nil {
		t.Fatal("stalled response accepted")
	}
	elapsed := time.Since(start)
	if elapsed < 2800*time.Millisecond || elapsed > 5*time.Second {
		t.Fatal("request budget", elapsed)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request did not join")
	}
}
