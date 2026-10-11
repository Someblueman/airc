package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
)

const routeAuthorToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func routeAuthorTokenFile(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "account.token")
	if err := os.WriteFile(path, []byte(routeAuthorToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBoundAuthorAndGuestNick(t *testing.T) {
	root := privateTemp(t)
	guest, err := LoadBoundAuthor(config.BoundAuthor{Nick: "alerts"})
	if err != nil || !guest.Valid() || guest.Author != (core.Author{Nick: "alerts"}) {
		t.Fatal(guest, err)
	}
	registered, err := LoadBoundAuthor(config.BoundAuthor{Nick: "alerts", AccountTokenFile: routeAuthorTokenFile(t, root)})
	if err != nil || !registered.Valid() || registered.Author != (core.Author{Nick: "alerts", Registered: true}) || registered.Credential.Token() != routeAuthorToken {
		t.Fatal(registered, err)
	}
	for _, b := range []config.BoundAuthor{{Nick: ""}, {Nick: "9lives"}, {Nick: "alerts", AccountTokenFile: filepath.Join(root, "missing")}} {
		if _, err := LoadBoundAuthor(b); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid bound author loaded", b, err)
		}
	}
	// Simple provider IDs keep their name; anything lossy gets a hash suffix,
	// so distinct GitHub logins or devices never share a guest nickname.
	for id, want := range map[string]string{"octocat": "gh-octocat", "a-b": "gh-a-b"} {
		if got := GuestNick("gh-", id, id); got != want || !protocol.ValidNick(got) {
			t.Fatal(id, got)
		}
	}
	for _, id := range []string{"dot.name", strings.Repeat("x", 40)} {
		got := GuestNick("gh-", id, id)
		if !protocol.ValidNick(got) || len(got) > 30 || !strings.HasPrefix(got, "gh-") || !strings.Contains(got, "|") {
			t.Fatal(id, got)
		}
	}
	if GuestNick("gh-", strings.Repeat("x", 40), strings.Repeat("x", 40)) == GuestNick("gh-", strings.Repeat("x", 41), strings.Repeat("x", 41)) {
		t.Fatal("long logins share a guest nickname")
	}
	if got := GuestNick("lora-", "70b3d57ed0000001", "70b3d57ed0000001"); got != "lora-70b3d57ed0000001" || !protocol.ValidNick(got) {
		t.Fatal(got)
	}
}
