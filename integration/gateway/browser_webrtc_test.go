//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
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

	"github.com/Someblueman/airc/pkg/irc"
)

func TestBrowserWebRTCProfilesAndModes(t *testing.T) {
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
	for _, name := range []string{"envelopes-fetch.js", "envelopes-webrtc.js", "events-fetch.js"} {
		name := name
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join("../..", "examples/gateway", name))
		})
	}
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "invalid report", http.StatusBadRequest)
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
	f := newBrowserWebRTCFixture(t, browser.URL)
	configJSON, err := json.Marshal(map[string]any{
		"base":      f.base,
		"bearer":    f.bearer,
		"audience":  f.credentials.realm,
		"signing":   f.credentials.browserSigningKey,
		"recipient": f.credentials.browserRecipientKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("testdata/browser-webrtc.html")
	if err != nil {
		t.Fatal(err)
	}
	page.Store(append(append([]byte(`<script type="application/json" id="config">`), configJSON...), append([]byte(`</script>`), html...)...))
	fmt.Println("BROWSER_FIXTURE_URL=" + browser.URL)
	report := f.waitBrowser(t, reports)

	want := map[string]bool{}
	for _, mode := range []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"} {
		want[mode] = true
	}
	want["reply"] = true
	want["live"] = true
	seen := map[string]*irc.MessageEvent{}
	deadline := time.After(10 * time.Second)
	for len(seen) < len(want) {
		select {
		case event, ok := <-f.observer.Events():
			if !ok {
				t.Fatal("native observer disconnected")
			}
			message, ok := event.(*irc.MessageEvent)
			if !ok || message.From != "fixture-author" || !strings.HasPrefix(message.Message, "browser WebRTC ") {
				continue
			}
			mode := strings.SplitN(strings.TrimPrefix(message.Message, "browser WebRTC "), "\n", 2)[0]
			if message.Message == "browser WebRTC reply\nwhole body 🙂" {
				mode = "reply"
			}
			if !want[mode] {
				continue
			}
			if seen[mode] != nil {
				t.Fatalf("browser WebRTC duplicated native message %q", message.Message)
			}
			seen[mode] = message
		case <-deadline:
			t.Fatalf("native observer missed WebRTC messages: got=%v", seen)
		}
	}
	for mode, message := range seen {
		body := "browser WebRTC " + mode + "\n" + strings.Repeat("x", 3450)
		if mode == "reply" {
			body = "browser WebRTC reply\nwhole body 🙂"
		} else if mode == "live" {
			body = "browser WebRTC live"
		}
		if message.Target != "#gateway-fixture" || message.From != "fixture-author" || message.ID == "" || message.Message != body {
			t.Fatalf("invalid native WebRTC message=%+v", message)
		}
	}
	modes := []string{"reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"}
	if len(report.MessageIDs) != len(modes) {
		t.Fatalf("browser message IDs=%v", report.MessageIDs)
	}
	for i, mode := range modes {
		if report.MessageIDs[i] != seen[mode].ID {
			t.Fatalf("browser %s ID=%q native=%q", mode, report.MessageIDs[i], seen[mode].ID)
		}
	}
	var reply *irc.MessageEvent
	for _, message := range seen {
		if message.Message == "browser WebRTC reply\nwhole body 🙂" {
			reply = message
		}
	}
	if reply == nil || reply.ReplyTo != report.MessageIDs[0] || reply.ThreadID != reply.ReplyTo {
		t.Fatalf("native WebRTC reply metadata missing: %+v", reply)
	}
	retained := nativeWebTransportMessages(t, f.socket, "#gateway-fixture")
	if len(retained) != len(want) {
		t.Fatalf("native WebRTC CHECK message count=%d want=%d", len(retained), len(want))
	}
	for _, record := range retained {
		observed := false
		for _, event := range seen {
			if record.ID == event.ID && record.From == event.From && record.Target == event.Target && record.Message == event.Message {
				observed = true
				break
			}
		}
		if !observed {
			t.Fatalf("native WebRTC CHECK record has no matching observer event: %+v", record)
		}
	}
	if len(report.HistoryIDs) != len(retained) {
		t.Fatalf("browser history IDs=%v native CHECK IDs=%d", report.HistoryIDs, len(retained))
	}
	for i, record := range retained {
		if report.HistoryIDs[i] != record.ID {
			t.Fatalf("browser history ID[%d]=%q native CHECK ID=%q", i, report.HistoryIDs[i], record.ID)
		}
	}
}

type browserWebRTCReport struct {
	Status     string   `json:"status"`
	MessageIDs []string `json:"message_ids"`
	HistoryIDs []string `json:"history_ids"`
}
