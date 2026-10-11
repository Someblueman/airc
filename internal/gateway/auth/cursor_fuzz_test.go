//go:build darwin || linux

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func FuzzCursorOpen(f *testing.F) {
	root := f.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		f.Fatal(err)
	}
	path := filepath.Join(root, "cursor.key")
	if err := CreateCredential(path); err != nil {
		f.Fatal(err)
	}
	c, err := NewCursorCodec(path)
	if err != nil {
		f.Fatal(err)
	}
	scope := CursorScope{Realm: "realm", Author: "a:alice", Operation: core.HistoryRead, Target: core.Target{Kind: "room", Name: "#room"}}
	position := strings.Repeat("a", 32)
	token, err := c.Seal(scope, position)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{token, "gc1.", position, token + "\n", "../state", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if after, err := c.Open(scope, input); err == nil && after != position {
			t.Fatal("unexpected authenticated position", after)
		}
	})
}
