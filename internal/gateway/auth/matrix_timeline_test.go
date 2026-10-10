//go:build unix

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestMatrixAuthenticatedTimelineBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "gap", "departure", "unknown_room", "same_cursor"} {
		t.Run(mode, func(t *testing.T) {
			since := "Opaque/+世界&user_id=@other"
			s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer matrix-secret" {
					t.Error("missing header token")
				}
				switch r.URL.Path {
				case "/_matrix/client/v3/account/whoami":
					_, _ = io.WriteString(w, `{"user_id":"@bridge:localhost"}`)
				case "/_matrix/client/v3/joined_rooms":
					_, _ = io.WriteString(w, `{"joined_rooms":["!room"]}`)
				case "/_matrix/client/v3/sync":
					if r.URL.Query().Get("since") != since || r.URL.Query().Get("user_id") != "" || r.URL.RequestURI() != matrixSyncPath(since, []string{"!room"}) {
						t.Error("wrong canonical sync request")
					}
					timeline := map[string]any{"limited": false, "events": []any{map[string]any{"type": "m.room.message", "event_id": "$event"}}}
					room := "!room"
					next := "Next/+世界"
					if mode == "unknown_room" {
						room = "!other"
					}
					if mode == "same_cursor" {
						next = since
					}
					groups := map[string]any{"join": map[string]any{room: map[string]any{"timeline": timeline}}}
					if mode == "gap" {
						timeline["limited"] = true
						timeline["prev_batch"] = "Before/+"
					}
					if mode == "departure" {
						delete(groups, "join")
						groups["leave"] = map[string]any{"!room": map[string]any{}}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"next_batch": next, "rooms": groups})
				default:
					t.Error("unexpected request path")
				}
			})
			p, err := s.ReadTimeline(context.Background(), since)
			if mode == "gap" {
				if err != nil || !p.Valid() || !p.HasGaps() || p.Cursor() != "Next/+世界" {
					t.Fatal("gap did not produce a usable page", p, err)
				}
				g := p.Gaps()
				if len(g) != 1 || g[0].Before != "Before/+" {
					t.Fatal("lost recovery position")
				}
				g[0].Before = "changed"
				if p.Gaps()[0].Before == "changed" {
					t.Fatal("mutable page gaps")
				}
				return
			}
			if mode != "valid" {
				if err == nil || p.Valid() || p.Cursor() != "" {
					t.Fatal("refusal exposed usable page")
				}
				if mode == "departure" {
					var loss *MatrixTimelineLoss
					if !errors.As(err, &loss) || !errors.Is(err, ErrMatrixTimelineLoss) {
						t.Fatal("missing explicit loss", err)
					}
				}
				return
			}
			if err != nil || !p.Valid() || p.Initial() || p.Cursor() != "Next/+世界" || p.Since() != since {
				t.Fatal(p, err)
			}
			refs := p.Events()
			if len(refs) != 1 || refs[0].Room != "!room" || refs[0].ID != "$event" {
				t.Fatal("wrong event references")
			}
			refs[0].ID = "$changed"
			if p.Events()[0].ID != "$event" {
				t.Fatal("mutable page")
			}
		})
	}
	if (VerifiedMatrixTimeline{}).Valid() || (VerifiedMatrixTimeline{}).Cursor() != "" {
		t.Fatal("zero page yields progression")
	}
	for _, path := range []string{"/_matrix/client/v3/sync?access_token=secret", "/_matrix/client/v3/sync?filter={}", matrixSyncPath("since", []string{"!room"}) + "&user_id=other"} {
		if matrixHTTPPath(path) {
			t.Fatal("unsafe sync query accepted")
		}
	}
	if matrixSyncPath("", []string{"!room", "!room"}) != "" || matrixSyncPath("bad\n", []string{"!room"}) != "" {
		t.Fatal("invalid sync profile accepted")
	}
}
