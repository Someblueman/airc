//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOfflineBundleChecksAllEntriesBeforeAdmission(t *testing.T) {
	f := newOfflineBundleFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	prefix := "offline exact body limit 🙂\n"
	first := core.Request{Version: 1, RequestID: "offline-first", Operation: core.Send, Target: room, Message: prefix + strings.Repeat("x", 4096-len(prefix))}
	for _, kind := range []string{"read", "duplicate", "body"} {
		r := core.Request{Version: 1, RequestID: "offline-refuse-" + kind, Operation: core.Send, Target: room, Message: "must never deliver"}
		token := f.credentials.token(t, r, false)
		switch kind {
		case "read":
			r = core.Request{Version: 1, RequestID: r.RequestID, Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
			token = f.credentials.token(t, r, false)
		case "duplicate":
			token = f.credentials.token(t, first, false)
		case "body":
			r.Message = strings.Repeat("x", 4097)
			token = f.credentials.token(t, r, false)
		}
		if report := offlineBundleCall(t, f, []string{f.credentials.token(t, first, false), token}, false); report.Version != 0 {
			t.Fatal("preflight refusal returned intake report", kind, report)
		}
		if _, err := os.Stat(filepath.Join(f.config.StateDir, "journal.sqlite")); !os.IsNotExist(err) {
			t.Fatal("preflight refusal opened journal", kind, err)
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 0 {
		t.Fatal("refused bundle delivered", native)
	}
	control := offlineBundleCall(t, f, []string{f.credentials.token(t, first, false)}, true)
	if len(control.Entries) != 1 || control.Entries[0].Result.State != core.Accepted {
		t.Fatal("matched valid control failed", control)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 1 || native[0].From != "fixture-author" || native[0].Message != first.Message {
		t.Fatal("wrong matched native control", native)
	}
}
