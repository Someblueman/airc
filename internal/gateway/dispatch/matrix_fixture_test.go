//go:build darwin || linux

package dispatch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func matrixDispatchProof(t *testing.T, c config.Config, sender, id, body string, stamp time.Time) auth.VerifiedMatrixMessage {
	t.Helper()
	cert := testtls.New(t)
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer private" {
			t.Error("missing authorSpec")
		}
		switch r.URL.Path {
		case "/_matrix/client/v3/account/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"user_id": "@bridge:localhost"})
		case "/_matrix/client/v3/joined_rooms":
			_ = json.NewEncoder(w).Encode(map[string]any{"joined_rooms": []string{"!external"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "m.room.message", "room_id": "!external", "sender": sender, "event_id": id, "origin_server_ts": stamp.UnixMilli(), "content": map[string]any{"msgtype": "m.text", "body": body}})
		}
	}))
	peer.TLS = cert.Server.Clone()
	peer.StartTLS()
	defer peer.Close()
	token := filepath.Join(filepath.Dir(c.StateDir), "matrix-token")
	if err := os.WriteFile(token, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.MatrixBridge{ID: "network", Address: peer.Listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Account: "@bridge:localhost", AccessTokenFile: token, FreshnessSeconds: 60,
		Routes: []config.MatrixRoute{{Room: "!external", Target: core.Target{Kind: "room", Name: "#Allowed"}, Operations: []core.Operation{core.Send, core.ContextRead}}}}
	server, err := auth.NewMatrixServer(c, b)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	proof, disposition, err := server.ReadMessage(t.Context(), "!external", id)
	if err != nil || disposition != "" {
		t.Fatal(err)
	}
	return proof
}
