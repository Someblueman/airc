//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

// matrixHomeserver is an httptest homeserver with one joined room whose
// history holds the given events (sender, body).
type matrixHomeserver struct {
	mu      sync.Mutex
	events  map[string]map[string]any
	order   []string
	notices []map[string]any
}

func (h *matrixHomeserver) add(id, sender, body string) {
	h.events[id] = map[string]any{"type": "m.room.message", "room_id": "!external", "sender": sender, "event_id": id,
		"origin_server_ts": time.Now().UnixMilli(), "content": map[string]any{"msgtype": "m.text", "body": body}}
	h.order = append(h.order, id)
}

func (h *matrixHomeserver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path, _ := url.PathUnescape(r.URL.EscapedPath())
	switch {
	case path == "/_matrix/client/v3/account/whoami":
		_, _ = io.WriteString(w, `{"user_id":"@bridge:localhost"}`)
	case path == "/_matrix/client/v3/joined_rooms":
		_, _ = io.WriteString(w, `{"joined_rooms":["!external"]}`)
	case path == "/_matrix/client/v3/sync":
		_ = json.NewEncoder(w).Encode(map[string]any{"next_batch": "s1", "rooms": map[string]any{}})
	case path == "/_matrix/client/v3/rooms/!external/messages":
		chunk := []any{}
		for _, id := range h.order {
			chunk = append(chunk, map[string]any{"event_id": id, "room_id": "!external", "type": "m.room.message"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"start": "h0", "chunk": chunk})
	case strings.HasPrefix(path, "/_matrix/client/v3/rooms/!external/event/"):
		_ = json.NewEncoder(w).Encode(h.events[strings.TrimPrefix(path, "/_matrix/client/v3/rooms/!external/event/")])
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/_matrix/client/v3/rooms/!external/send/m.room.message/"):
		var notice map[string]any
		_ = json.NewDecoder(r.Body).Decode(&notice)
		h.notices = append(h.notices, notice)
		_, _ = io.WriteString(w, `{"event_id":"$notice`+string(rune('a'+len(h.notices)))+`"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// A Matrix sender's plain message is a send as the author derived from its
// user ID; a read request is answered with one plain notice replying to it.
func TestMatrixPlainMessagesAndReadReplies(t *testing.T) {
	f := newBridgeRoomFixture(t, "!external", core.Send)
	h := &matrixHomeserver{events: map[string]map[string]any{}}
	h.add("$plain", "@alice:localhost", "hello from matrix 世界")
	h.add("$read", "@alice:localhost", `AIRC-READ/1 {"operation":"history.read","correlation_id":"m1"}`)
	h.add("$foreign", "@alice:elsewhere.org", "from another homeserver")
	h.add("$echo", "@bridge:localhost", "the bridge's own notice")
	cert := testtls.New(t)
	peer := httptest.NewUnstartedServer(h)
	peer.TLS = cert.Server.Clone()
	peer.StartTLS()
	defer peer.Close()
	token := filepath.Join(filepath.Dir(f.c.StateDir), "matrix-token")
	if err := os.WriteFile(token, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.MatrixBridge{ID: "matrix", Address: peer.Listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Account: "@bridge:localhost", AccessTokenFile: token, FreshnessSeconds: 60, Routes: f.routes}
	server, err := auth.NewMatrixServer(f.c, b)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	requests := make(chan struct{}, 1)
	for range 3 { // sync anchor, the room's history page, then the cursor
		if _, err := AdvanceMatrix(t.Context(), server, f.d, f.s, requests); err != nil {
			t.Fatal(err)
		}
	}
	job, err := f.guestJob(t, "mx-alice", matrixRequestID("$plain"), 0)
	if err != nil || job.Request.Message != "hello from matrix 世界" || *job.Request.Target != f.target {
		t.Fatal("plain message", job, err)
	}
	foreign, err := f.guestJob(t, "mx-alice", matrixRequestID("$foreign"), 0)
	if err == nil {
		t.Fatal("another homeserver's alice posted as the local guest", foreign)
	}
	if job, err := f.guestJob(t, auth.GuestNick("mx-", "alice:elsewhere.org", "@alice:elsewhere.org"), matrixRequestID("$foreign"), 0); err != nil || job.Request.Message != "from another homeserver" {
		t.Fatal("foreign sender's guest", job, err)
	}
	if _, err := f.guestJob(t, "mx-alice", matrixRequestID("$read"), 0); err == nil {
		t.Fatal("read request posted")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notices) != 1 {
		t.Fatal("notices", h.notices)
	}
	notice := h.notices[0]
	relates, _ := notice["m.relates_to"].(map[string]any)
	reply, _ := relates["m.in_reply_to"].(map[string]any)
	var result BridgeResult
	if notice["msgtype"] != "m.notice" || reply["event_id"] != "$read" || json.Unmarshal([]byte(notice["body"].(string)), &result) != nil {
		t.Fatal("notice shape", notice)
	}
	if result.Protocol != "matrix" || result.Author != "mx-alice" || result.CorrelationID != "m1" || result.Error == nil || result.Error.Code != core.UnsupportedOperation {
		t.Fatal("read reply", result)
	}
}

func matrixRequestID(event string) string {
	return ircOwnerRequestIDParts("airc-matrix-message-v1", "matrix", event)
}
