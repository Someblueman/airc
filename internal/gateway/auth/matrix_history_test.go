//go:build unix

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatrixVerifiedHistoryBoundaries(t *testing.T) {
	from, to := "Opaque/+世界&user_id=@other", "boundary/+世界"
	for _, mode := range []string{"valid", "initial", "empty_more", "exhausted", "wrong_start", "wrong_room", "no_progress"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			start := from
			requested := from
			if mode == "initial" {
				requested = ""
			}
			s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if matrixSyncPreamble(w, r) {
					return
				}
				if r.URL.RequestURI() != matrixHistoryPath("!room", requested, to, "b") || r.Header.Get("Authorization") != "Bearer matrix-secret" {
					t.Error("history request changed", r.URL.RequestURI())
				}
				event := map[string]any{"event_id": "$event", "room_id": "!room", "type": "m.room.message"}
				page := map[string]any{"start": start, "end": "next", "chunk": []any{event}}
				switch mode {
				case "wrong_start":
					page["start"] = "other"
				case "wrong_room":
					event["room_id"] = "!other"
				case "no_progress":
					page["end"] = start
				case "empty_more":
					page["chunk"] = []any{}
				case "exhausted":
					delete(page, "end")
					page["chunk"] = []any{}
				}
				_ = json.NewEncoder(w).Encode(page)
			})
			p, err := s.ReadHistory(context.Background(), "!room", requested, to, "b")
			refused := mode == "wrong_start" || mode == "wrong_room" || mode == "no_progress"
			if refused {
				if err == nil || p.Valid() {
					t.Fatal("refusal minted page", p, err)
				}
				return
			}
			if err != nil || !p.Valid() || p.Room() != "!room" || p.From() != requested || p.To() != to || p.Direction() != "b" || p.Start() != from || p.Exhausted() != (mode == "exhausted") {
				t.Fatal(p, err)
			}
			if events := p.Events(); len(events) > 0 {
				events[0].ID = "$changed"
				if p.Events()[0].ID != "$event" {
					t.Fatal("mutable provenance")
				}
			}
			before := calls.Load()
			if p, err := s.ReadHistory(context.Background(), "!unknown", from, to, "f"); err == nil || p.Valid() {
				t.Fatal("unknown room accepted")
			}
			if calls.Load() != before {
				t.Fatal("unknown room contacted server")
			}
		})
	}
	var zero VerifiedMatrixHistory
	if zero.Valid() || zero.Exhausted() || zero.Start() != "" || zero.End() != "" || len(zero.Events()) != 0 {
		t.Fatal("zero page granted progress")
	}
	path := matrixHistoryPath("!Opaque/世界", from, to, "f")
	if path == "" || !matrixHistoryHTTPPath(path) {
		t.Fatal("canonical history path refused")
	}
	for _, bad := range []string{path + "&access_token=secret", path + "&user_id=@other", path + "&limit=128", path + "&dir=b", "/_matrix/client/v3/rooms/!room/messages?dir=f&limit=129"} {
		if matrixHTTPPath(bad) {
			t.Fatal("unsafe query admitted", bad)
		}
	}
	if matrixHistoryPath("!room", from, from, "f") != "" || matrixHistoryPath("!room", from, to, "sideways") != "" {
		t.Fatal("invalid traversal allowed")
	}
}

func TestMatrixHistoryCancellationJoinsRequest(t *testing.T) {
	entered, done := make(chan struct{}), make(chan struct{})
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if matrixSyncPreamble(w, r) {
			return
		}
		close(entered)
		<-r.Context().Done()
		close(done)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		p, err := s.ReadHistory(ctx, "!room", "from", "", "f")
		if p.Valid() {
			err = nil
		}
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled history minted page")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("history request not joined")
	}
	// Closed owners cannot make account or history requests.
	s.Close()
	if p, err := s.ReadHistory(context.Background(), "!room", "from", "", "f"); err == nil || p.Valid() {
		t.Fatal("closed owner minted page")
	}
}
