package gateway_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestHTTPDirectoryPublicCards maps directory to the native public cards with
// their profile, presence and connection facts, and publishes its scope and
// limits in capabilities.
func TestHTTPDirectoryPublicCards(t *testing.T) {
	f := gatewayHTTPFixture(t, false)
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
	if json.Unmarshal(data, &snapshot) != nil || status != 200 || proto != 1 || snapshot.Version != 1 {
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
	if _, err := f.journal.LookupAuthor(t.Context(), f.identity, "directory"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("directory created delivery job", err)
	}
	status, _, data = httpCall(t, f, http.MethodGet, "/v1/capabilities", f.token, nil)
	var capabilities dispatch.Capabilities
	if status != 200 || json.Unmarshal(data, &capabilities) != nil || capabilities.DirectoryScope != "public_global" || capabilities.MaxDirectoryBytes != backend.MaxDirectoryBytes || capabilities.MaxDirectoryCards != 256 {
		t.Fatal("directory scope/limits not discoverable", string(data))
	}
}
