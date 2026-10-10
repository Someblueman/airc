//go:build darwin || linux

package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOfflineBundleAuthenticatesAllEntriesBeforeAdmission(t *testing.T) {
	f := newOfflineBundleFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	prefix := "offline exact body limit 🙂\n"
	first := core.Request{Version: 1, RequestID: "offline-first", Operation: core.Send, Target: room, Message: prefix + strings.Repeat("x", 4096-len(prefix))}
	for _, kind := range []string{"signature", "expired", "future", "audience", "target", "encrypted", "read", "duplicate", "body"} {
		r := core.Request{Version: 1, RequestID: "offline-refuse-" + kind, Operation: core.Send, Target: room, Message: "must never deliver"}
		token := f.credentials.token(t, r, false)
		now := time.Now().Unix()
		switch kind {
		case "signature":
			parts := strings.Split(token, ".")
			signature, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil {
				t.Fatal(err)
			}
			signature[0] ^= 1
			parts[2] = base64.RawURLEncoding.EncodeToString(signature)
			token = strings.Join(parts, ".")
		case "expired":
			token = f.credentials.validityToken(t, r, now-600, now-300, false)
		case "future":
			token = f.credentials.validityToken(t, r, now+300, now+600, false)
		case "audience":
			other := *f.credentials
			other.realm = "other-realm"
			token = other.token(t, r, false)
		case "target":
			r.Target = &core.Target{Kind: "room", Name: "#not-authorized"}
			token = f.credentials.token(t, r, false)
		case "encrypted":
			token = f.credentials.token(t, r, true)
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
	for _, revoked := range []bool{true, false} {
		f.config.Principals[0].Revoked = revoked
		data, err := json.Marshal(f.config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if revoked {
			if report := offlineBundleCall(t, f, []string{f.credentials.token(t, first, false)}, false); report.Version != 0 {
				t.Fatal("revoked sender returned intake", report)
			}
			if _, err := os.Stat(filepath.Join(f.config.StateDir, "journal.sqlite")); !os.IsNotExist(err) {
				t.Fatal("revoked sender opened journal", err)
			}
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 0 {
		t.Fatal("refused bundle delivered", native)
	}
	control := offlineBundleCall(t, f, []string{f.credentials.token(t, first, false)}, true)
	if len(control.Entries) != 1 || control.Entries[0].Result.State != core.Accepted {
		t.Fatal("matched valid control failed", control)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 1 || native[0].From != "fixture-author" || native[0].Message != first.Message {
		t.Fatal("wrong matched native control", native)
	}
}
