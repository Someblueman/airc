package gateway_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestBrowserDetachedCryptoAndEvents(t *testing.T) {
	if os.Getenv("AGW_BROWSER_PROBE") != "1" {
		t.Skip("requires interactive real browser")
	}
	reports := make(chan string, 1)
	var page atomic.Value
	page.Store([]byte{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page.Load().([]byte))
	})
	for _, name := range []string{"events-fetch.js", "envelopes-fetch.js"} {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join("../..", "examples/gateway", name))
		})
	}
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid report", 400)
			return
		}
		select {
		case reports <- string(data):
		default:
		}
		w.WriteHeader(204)
	})
	browser := httptest.NewServer(mux)
	defer browser.Close()
	f := httpGatewayFixture(t, false, []string{browser.URL}, true, true)
	cfg, err := json.Marshal(map[string]any{"base": f.server.URL, "audience": f.credentials.realm, "signing": f.credentials.browserSigningKey, "recipient": f.credentials.browserRecipientKey})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/browser-detached.html")
	if err != nil {
		t.Fatal(err)
	}
	page.Store(append(append([]byte(`<script type="application/json" id="config">`), cfg...), append([]byte(`</script>`), html...)...))
	fmt.Println("BROWSER_FIXTURE_URL=" + browser.URL)
	select {
	case report := <-reports:
		if report != "PASS" {
			t.Fatal(report)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("browser crypto probe timed out")
	}
	seen := map[string]*irc.MessageEvent{}
	for len(seen) < 6 {
		message := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.From == "fixture-author" })
		if seen[message.Message] != nil {
			t.Fatal("browser retry produced a duplicate", message.Message)
		}
		seen[message.Message] = message
	}
	for _, profile := range []string{"signed", "encrypted"} {
		for _, kind := range []string{"send", "reply", "live"} {
			if seen["browser "+profile+" "+kind+"\nwhole body 🙂"] == nil {
				t.Fatal("independent native observer missed browser message", profile, kind)
			}
		}
		parent := seen["browser "+profile+" send\nwhole body 🙂"]
		reply := seen["browser "+profile+" reply\nwhole body 🙂"]
		if reply.ReplyTo != parent.ID || reply.ThreadID != parent.ID {
			t.Fatal("browser reply lost native parent/thread", profile, reply)
		}
	}
}
