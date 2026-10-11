//go:build unix

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestMatrixVerifiedExactMessage(t *testing.T) {
	for _, mode := range []string{"valid", "mapped", "read", "bad_read", "guests_off", "loop", "ID", "room", "missing_room", "redacted", "stale", "future"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			id := "$Opaque/ID+世界"
			origin := time.Now().UnixMilli()
			s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer matrix-secret" {
					t.Error("wrong authority")
				}
				switch r.URL.Path {
				case "/_matrix/client/v3/account/whoami":
					_, _ = io.WriteString(w, `{"user_id":"@bridge:localhost"}`)
				case "/_matrix/client/v3/joined_rooms":
					_, _ = io.WriteString(w, `{"joined_rooms":["!room"]}`)
				default:
					if r.URL.EscapedPath() != matrixEventPath("!room", id) {
						t.Error("event path changed opaque ID", r.URL.EscapedPath())
					}
					e := map[string]any{"type": "m.room.message", "room_id": "!room", "sender": "@alice:localhost", "event_id": id, "origin_server_ts": origin, "content": map[string]any{"msgtype": "m.text", "body": "  café 世界\nnext  "}}
					switch mode {
					case "stale":
						e["origin_server_ts"] = origin - 61000
					case "future":
						e["origin_server_ts"] = origin + 60000
					case "mapped":
						e["sender"] = "@carol:localhost"
					case "read":
						e["content"] = map[string]any{"msgtype": "m.text", "body": `AIRC-READ/1 {"operation":"history.read","limit":2}`}
					case "bad_read":
						e["content"] = map[string]any{"msgtype": "m.text", "body": `AIRC-READ/1 {"operation":"send"}`}
					case "loop":
						e["sender"] = "@bridge:localhost"
					case "ID":
						e["event_id"] = "$other"
					case "room":
						e["room_id"] = "!other"
					case "missing_room":
						delete(e, "room_id")
					case "redacted":
						e["unsigned"] = map[string]any{"redacted_because": map[string]any{}}
					}
					_ = json.NewEncoder(w).Encode(e)
				}
			})
			if mode == "guests_off" {
				s.authors.guests = false
			}
			m, disposition, err := s.ReadMessage(context.Background(), "!room", id)
			e := m.Event()
			switch mode {
			case "mapped":
				if err != nil || e.Author().Author != (core.Author{Nick: "carol", Registered: true}) || e.Author().Credential.Empty() || e.Request().Operation != core.Send {
					t.Fatal("mapped account", err, e.Author())
				}
				return
			case "read":
				if err != nil || !m.IsRead() || !e.Allowed() || e.Request().Operation != core.HistoryRead || e.Request().Limit != 2 || e.Author().Author.Nick != "mx-alice" {
					t.Fatal("read", err, e.Request())
				}
				return
			case "bad_read":
				if err != nil || !m.IsRead() || !e.Refused() {
					t.Fatal("malformed read must be answered, not posted", err)
				}
				return
			case "guests_off":
				if err != nil || disposition != "unmapped" || m.Valid() {
					t.Fatal("guest admitted with guests off", disposition, err)
				}
				return
			case "valid":
			default:
				if err == nil && disposition == "" || m.Valid() {
					t.Fatal("refusal minted proof")
				}
				return
			}
			if err != nil || disposition != "" || !m.Valid() || e.Author().Author != (core.Author{Nick: "mx-alice"}) || e.Author().Source != "bridge:matrix:mx-alice" || m.Author() != "@alice:localhost" || m.Room() != "!room" || m.Body() != "  café 世界\nnext  " || e.Request().Message != m.Body() || m.RequestID() == "" {
				t.Fatal(m, err)
			}
			occurred := time.UnixMilli(origin).UTC()
			if !m.Occurred().Equal(occurred) || !m.ReplayDeadline().Equal(occurred.Add(s.freshness)) {
				t.Fatal("origin deadline lost")
			}
			other := m
			other.room = "!other"
			if other.RequestID() != m.RequestID() {
				t.Fatal("globally unique event ID became room scoped")
			}
			if e.Target() != s.routes["!room"].target {
				t.Fatal("wrong native target")
			}
			if calls.Load() != 3 {
				t.Fatal("unexpected request count")
			}
			if p, _, err := s.ReadMessage(context.Background(), "!unprovisioned", id); err == nil || p.Valid() {
				t.Fatal("unprovisioned room proof")
			}
			if calls.Load() != 3 {
				t.Fatal("unprovisioned room contacted server")
			}
		})
	}
	if (VerifiedMatrixMessage{}).Valid() || (VerifiedMatrixMessage{}).RequestID() != "" {
		t.Fatal("zero proof grants authority")
	}
}

func TestMatrixEventCancellationCannotMintProof(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan struct{})
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/_matrix/client/v3/account/whoami":
			_, _ = io.WriteString(w, `{"user_id":"@bridge:localhost"}`)
		case "/_matrix/client/v3/joined_rooms":
			_, _ = io.WriteString(w, `{"joined_rooms":["!room"]}`)
		default:
			close(entered)
			<-r.Context().Done()
			close(done)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		m, _, err := s.ReadMessage(ctx, "!room", "$event")
		if m.Valid() {
			err = nil
		}
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled event granted proof")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event request did not join")
	}
}
