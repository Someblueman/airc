//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func browserCertificateHash(t *testing.T, path string) string {
	t.Helper()
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("missing server certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(cert.Raw)
	return base64.StdEncoding.EncodeToString(hash[:])
}

func TestBrowserWebTransportCryptoAndDatagrams(t *testing.T) {
	if os.Getenv("AGW_BROWSER_PROBE") != "1" {
		t.Skip("requires interactive real browser")
	}
	var page atomic.Value
	page.Store([]byte{})
	reports := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page.Load().([]byte))
	})
	for _, name := range []string{"envelopes-fetch.js", "envelopes-webtransport.js", "events-fetch.js"} {
		name := name
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join("../..", "examples/gateway", name))
		})
	}
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid report", 400)
			return
		}
		select {
		case reports <- string(body):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	browser := httptest.NewServer(mux)
	defer browser.Close()
	cert := testtls.NewP256(t)
	f := newWebTransportFixtureWith(t, []string{browser.URL}, &cert, nil)
	// Interactive browser startup can outlast the native test client's ten-second
	// idle read limit. Install one long-lived observer before printing the URL.
	_ = f.observer.Close()
	_ = f.session.CloseWithError(0, "browser takes over")
	_ = f.transport.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	observer, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: f.config.Bindings[0].Address, Nick: "webtransport-browser-observer", ReadTimeout: 120 * time.Second, WriteTimeout: time.Second})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	f.observer = observer
	t.Cleanup(func() { _ = f.observer.Close() })
	if err := f.observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, f.observer, func(event *irc.JoinEvent) bool { return event.Agent == "webtransport-browser-observer" })
	configJSON, err := json.Marshal(map[string]any{"base": "https://" + f.address + "/v1/webtransport", "audience": f.credentials.realm, "signing": f.credentials.browserSigningKey, "recipient": f.credentials.browserRecipientKey, "leaf_hash": browserCertificateHash(t, f.config.Listeners[0].TLSCertFile), "operations": dispatch.ImplementedOperations()})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/browser-webtransport.html")
	if err != nil {
		t.Fatal(err)
	}
	page.Store(append(append([]byte(`<script type="application/json" id="config">`), configJSON...), append([]byte(`</script>`), html...)...))
	fmt.Println("BROWSER_FIXTURE_URL=" + browser.URL)
	select {
	case report := <-reports:
		if report != "PASS" {
			t.Fatal(report)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("browser WebTransport probe timed out")
	}
	want := map[string]bool{"browser WebTransport send 🙂": true, "browser WebTransport reply": true, "browser WebTransport live": true, "browser datagram " + strings.Repeat("x", 1200): true}
	seen := make(map[string]*irc.MessageEvent)
	deadline := time.After(8 * time.Second)
	for len(seen) < len(want) {
		select {
		case event, ok := <-f.observer.Events():
			if !ok {
				t.Fatal("native observer disconnected")
			}
			message, ok := event.(*irc.MessageEvent)
			if !ok || message.From != "fixture-author" || !want[message.Message] {
				continue
			}
			if seen[message.Message] != nil {
				t.Fatalf("browser WebTransport duplicated native message %q", message.Message)
			}
			seen[message.Message] = message
		case <-deadline:
			t.Fatalf("native observer missed browser WebTransport messages: got=%v", seen)
		}
	}
	retained := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if len(retained) != 4 {
		t.Fatalf("native CHECK message count=%d want=4", len(retained))
	}
	for _, message := range retained {
		if !want[message.Message] || message.From != "fixture-author" || message.Target != "#gateway-fixture" {
			t.Fatalf("unexpected native CHECK record=%+v", message)
		}
	}
	parent, reply := seen["browser WebTransport send 🙂"], seen["browser WebTransport reply"]
	if parent.Target != "#gateway-fixture" || parent.From != "fixture-author" || reply.ReplyTo != parent.ID || reply.ThreadID != parent.ID || seen["browser WebTransport live"].Target != "#gateway-fixture" {
		t.Fatalf("native WebTransport message/linkage mismatch: parent=%+v reply=%+v live=%+v", parent, reply, seen["browser WebTransport live"])
	}
}
