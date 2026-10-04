//go:build darwin || linux

package gateway_test

import (
	"context"
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

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBrowserWebSocketCryptoAndLiveWatch(t *testing.T) {
	if os.Getenv("AGW_BROWSER_PROBE") != "1" {
		t.Skip("requires interactive real browser")
	}
	reports := make(chan string, 1)
	evidence := make(chan []core.ReplySignalAcknowledgement, 1)
	var page atomic.Value
	page.Store([]byte{})
	mux := http.NewServeMux()
	const fixtureCookie = "agw-websocket-fixture"
	mux.HandleFunc("/v1/socket", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(fixtureCookie)
		_ = json.NewEncoder(w).Encode(err == nil && cookie.Value == "not-an-airc-credential")
	})
	mux.HandleFunc("/clear-cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: fixtureCookie, Value: "", Path: "/v1/socket", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(204)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.SetCookie(w, &http.Cookie{Name: fixtureCookie, Value: "not-an-airc-credential", Path: "/v1/socket", MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		_, _ = w.Write(page.Load().([]byte))
	})
	for _, name := range []string{"envelopes-fetch.js", "events-fetch.js", "envelopes-websocket.js"} {
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
	mux.HandleFunc("/evidence", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid evidence", 400)
			return
		}
		var values []core.ReplySignalAcknowledgement
		if err := json.Unmarshal(data, &values); err != nil || len(values) != 2 {
			http.Error(w, "invalid evidence", 400)
			return
		}
		select {
		case evidence <- values:
		default:
		}
		w.WriteHeader(204)
	})
	browser := httptest.NewServer(mux)
	defer browser.Close()
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", []string{browser.URL})
	_ = f.conn.CloseNow()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	observer, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: f.socket, Nick: "fixture-observer", ReadTimeout: 120 * time.Second, WriteTimeout: time.Second})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	if err = observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	cfg, err := json.Marshal(map[string]any{"base": f.base, "audience": f.credentials.realm, "signing": f.credentials.browserSigningKey, "recipient": f.credentials.browserRecipientKey})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/browser-websocket.html")
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
		t.Fatal("browser WebSocket probe timed out")
	}
	seen := map[string]*irc.MessageEvent{}
	var signals []*irc.SignalEvent
	deadline := time.After(8 * time.Second)
	for len(seen) < 4 || len(signals) < 2 {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("native observer disconnected")
			}
			switch value := event.(type) {
			case *irc.MessageEvent:
				if value.From != "fixture-author" {
					continue
				}
				if seen[value.Message] != nil {
					t.Fatal("browser retry produced duplicate native message", value.Message)
				}
				seen[value.Message] = value
			case *irc.SignalEvent:
				if value.From == "fixture-author" && (value.Action == "prepare" || value.Action == "cancel") {
					signals = append(signals, value)
				}
			}
		case <-deadline:
			t.Fatalf("native observer did not receive browser messages/signals: messages=%d signals=%d", len(seen), len(signals))
		}
	}
	var saved []core.ReplySignalAcknowledgement
	select {
	case saved = <-evidence:
	case <-time.After(2 * time.Second):
		t.Fatal("browser reply signal evidence missing")
	}
	if len(saved) != 2 || len(signals) != 2 {
		t.Fatalf("reply signal evidence count mismatch: saved=%d native=%d", len(saved), len(signals))
	}
	for i, ack := range saved {
		signal := signals[i]
		if ack.Action != signal.Action || ack.NativeTarget != signal.Target || ack.ParentID != signal.ID || ack.From != signal.From || ack.AccountID != signal.AccountID || ack.Note != signal.Text || !ack.ExpiresAt.Equal(signal.ExpiresAt) {
			t.Fatalf("browser reply signal ACK/native mismatch: ack=%+v signal=%+v", ack, signal)
		}
	}
	for _, kind := range []string{"send", "reply", "live"} {
		if seen["browser websocket "+kind+"\nwhole body 🙂"] == nil {
			t.Fatal("observer missed native browser message", kind)
		}
	}
	parent, reply := seen["browser websocket send\nwhole body 🙂"], seen["browser websocket reply\nwhole body 🙂"]
	if reply.ReplyTo != parent.ID || reply.ThreadID != parent.ID {
		t.Fatal("browser WebSocket lost reply/thread facts")
	}
	reaction := seen["agree"]
	if reaction == nil || reaction.Reaction != "agree" || reaction.ReplyTo != parent.ID || reaction.ThreadID != parent.ID || reaction.AccountID != parent.AccountID || reaction.RequestID != "" {
		t.Fatal("browser WebSocket lost native reaction facts", reaction)
	}
	f.stop()
}
