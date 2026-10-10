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
	for _, mode := range []string{"valid", "unmapped", "loop", "ID", "room", "missing_room", "redacted", "stale", "future"} {
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
					case "unmapped":
						e["sender"] = "@other:localhost"
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
			m, disposition, err := s.ReadMessage(context.Background(), "!room", id)
			if mode != "valid" {
				if err == nil && disposition == "" || m.Valid() {
					t.Fatal("refusal minted proof")
				}
				return
			}
			if err != nil || disposition != "" || !m.Valid() || m.Identity().ID() != "device" || m.Author() != "@alice:localhost" || m.Room() != "!room" || m.Body() != "  café 世界\nnext  " || m.RequestID() == "" {
				t.Fatal(m, err)
			}
			checkMatrixFingerprint(t, m, origin, s.freshness)
			other := m
			other.room = "!other"
			if other.RequestID() != m.RequestID() {
				t.Fatal("globally unique event ID became room scoped")
			}
			other.network = "other-network"
			if other.RequestID() == m.RequestID() {
				t.Fatal("configured network omitted")
			}
			ops := m.Operations()
			ops[0] = core.HistoryRead
			if m.Operations()[0] != core.Send {
				t.Fatal("mutable grants")
			}
			if m.Target() != s.grants[[2]string{"@alice:localhost", "!room"}].target {
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
