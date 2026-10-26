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

func TestBrowserJSONRPCFetchAndLiveWatch(t *testing.T) {
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
	for _, name := range []string{"rpc-fetch.js", "events-fetch.js"} {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join("..", "examples", name))
		})
	}
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid browser report", 400)
			return
		}
		select {
		case reports <- string(data):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	browser := httptest.NewServer(mux)
	defer browser.Close()
	f := gatewayHTTPOriginFixture(t, false, []string{browser.URL}, true)
	cfg, err := json.Marshal(map[string]any{"base": f.server.URL, "author": map[string]string{"nick": fixtureAuthor, "token": fixtureAccount}})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/browser-rpc.html")
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
		t.Fatal("browser RPC probe timed out")
	}
	seen := make(map[string]*irc.MessageEvent)
	for len(seen) < 4 {
		message := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.From == "fixture-author" })
		if seen[message.Message] != nil {
			t.Fatal("RPC replay produced native duplicate", message.Message)
		}
		seen[message.Message] = message
	}
	for _, text := range []string{"RPC browser parent\nwhole body 🙂", "RPC browser reply", "RPC browser live", "RPC browser notification"} {
		if seen[text] == nil {
			t.Fatal("independent native observer missed", text)
		}
	}
	parent, reply := seen["RPC browser parent\nwhole body 🙂"], seen["RPC browser reply"]
	if reply.ReplyTo != parent.ID || reply.ThreadID != parent.ID {
		t.Fatal("RPC browser reply lost native linkage", reply)
	}
}
