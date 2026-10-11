package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestEventsWatchCursorCacheReusesUnchangedPosition(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(path); err != nil {
		t.Fatal(err)
	}
	codec, err := auth.NewCursorCodec(path)
	if err != nil {
		t.Fatal(err)
	}
	scope := auth.CursorScope{Realm: "realm", Author: "a:alice", Operation: core.EventsWatch, Target: core.Target{Kind: "room", Name: "#room"}}
	d := &Dispatcher{cursors: codec}
	cache := &eventsCursorCache{}
	position := strings.Repeat("a", 32)
	first, err := d.sealEventsPage(scope, backend.HistoryPage{Cursor: position, Status: "ok"}, cache)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.sealEventsPage(scope, backend.HistoryPage{Cursor: position, Status: "ok"}, cache)
	if err != nil || first.Cursor != second.Cursor {
		t.Fatalf("unchanged position changed token: first=%q second=%q err=%v", first.Cursor, second.Cursor, err)
	}
	advanced, err := d.sealEventsPage(scope, backend.HistoryPage{Cursor: strings.Repeat("b", 32), Status: "ok"}, cache)
	if err != nil || advanced.Cursor == first.Cursor {
		t.Fatalf("advanced position reused token: first=%q advanced=%q err=%v", first.Cursor, advanced.Cursor, err)
	}
	if raw, err := codec.Open(scope, second.Cursor); err != nil || raw != position {
		t.Fatalf("cached token does not open to position: raw=%q err=%v", raw, err)
	}
	legacy1, err := d.sealPage(scope, backend.HistoryPage{Cursor: position, Status: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	legacy2, err := d.sealPage(scope, backend.HistoryPage{Cursor: position, Status: "ok"})
	if err != nil || legacy1.Cursor == legacy2.Cursor {
		t.Fatalf("legacy watch sealing unexpectedly cached: first=%q second=%q err=%v", legacy1.Cursor, legacy2.Cursor, err)
	}
}
