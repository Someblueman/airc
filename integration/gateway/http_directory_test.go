package gateway_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPDirectoryPublicCardsBoundsAndRevocation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			f := gatewayHTTPFixture(t, h2)
			const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			author := dial(t, f.socket, "fixture-author", account, false, true)
			if err := author.UpdateProfile(map[string]string{"model": "fixture model", "workspace": "workspace 🙂", "tools": "native SDK", "about": "public description"}); err != nil {
				t.Fatal(err)
			}
			next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
			if err := author.SetPresence("thinking", "self-reported activity", time.Minute); err != nil {
				t.Fatal(err)
			}
			next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
			status, proto, data := httpCall(t, f, http.MethodGet, "/v1/directory?limit=256", f.token, nil)
			var snapshot backend.DirectorySnapshot
			if json.Unmarshal(data, &snapshot) != nil || status != 200 || proto != 1 && !h2 || proto != 2 && h2 || snapshot.Version != 1 {
				t.Fatal("directory transport failed", status, proto, string(data))
			}
			foundAuthor, foundObserver := false, false
			for _, card := range snapshot.Agents {
				if card.Nick == "fixture-author" {
					foundAuthor = true
					if card.Model != "fixture model" || card.Workspace != "workspace 🙂" || card.Tools != "native SDK" || card.About != "public description" || card.State != "thinking" || card.Note != "self-reported activity" || card.ExpiresAt.IsZero() || card.LastSeen.IsZero() || card.Connected {
						t.Fatal("directory changed profile/presence facts", card)
					}
				}
				if card.Nick == "fixture-observer" {
					foundObserver = true
					if !card.Connected {
						t.Fatal("persistent observer missing presence")
					}
				}
			}
			if !foundAuthor || !foundObserver {
				t.Fatal("public directory omitted native cards", snapshot)
			}
			for _, test := range []struct {
				path, token string
				status      int
			}{
				{"/v1/directory?limit=1", f.token, 429}, {"/v1/directory?limit=0", f.token, 400}, {"/v1/directory?limit=257", f.token, 400},
				{"/v1/directory?limit=2&limit=3", f.token, 400}, {"/v1/directory?target=%23room", f.token, 400}, {"/v1/directory?cursor=raw", f.token, 400},
				{"/v1/directory", f.other, 403},
			} {
				status, _, data := httpCall(t, f, http.MethodGet, test.path, test.token, nil)
				if status != test.status {
					t.Fatal("directory denial failed", test, status, string(data))
				}
				var result map[string]json.RawMessage
				_ = json.Unmarshal(data, &result)
				if _, partial := result["agents"]; partial {
					t.Fatal("directory error exposed partial snapshot")
				}
			}
			if _, err := f.journal.Lookup(t.Context(), f.identity, "directory"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("directory created delivery job", err)
			}
			status, _, data = httpCall(t, f, http.MethodGet, "/v1/capabilities", f.token, nil)
			var capabilities dispatch.Capabilities
			if status != 200 || json.Unmarshal(data, &capabilities) != nil || capabilities.DirectoryScope != "public_global" || capabilities.MaxDirectoryBytes != backend.MaxDirectoryBytes || capabilities.MaxDirectoryCards != 256 {
				t.Fatal("directory scope/limits not discoverable", string(data))
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			status, _, _ = httpCall(t, f, http.MethodGet, "/v1/directory", f.token, nil)
			if status != 403 {
				t.Fatal("directory bypassed revocation", status)
			}
		})
	}
}
