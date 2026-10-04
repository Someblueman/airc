//go:build darwin || linux

package auth

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCursorCodecScopedTamperRestartAndRotation(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cursor.key")
	if err := CreateCredential(path); err != nil {
		t.Fatal(err)
	}
	c, err := NewCursorCodec(path)
	if err != nil {
		t.Fatal(err)
	}
	scope := CursorScope{Realm: "realm", Principal: "principal", Binding: "binding", Operation: core.HistoryRead, Target: core.Target{Kind: "room", Name: "#room"}}
	position := strings.Repeat("a", 32)
	token, err := c.Seal(scope, position)
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.Seal(scope, position)
	if err != nil || other == token || strings.Contains(token, position) {
		t.Fatal("nonce/opacity framing failed", err)
	}
	restarted, err := NewCursorCodec(path)
	if err != nil {
		t.Fatal(err)
	}
	if after, err := restarted.Open(scope, token); err != nil || after != position {
		t.Fatal("cursor did not survive snapshot restart", after, err)
	}
	for _, change := range []func(*CursorScope){
		func(s *CursorScope) { s.Realm = "other" }, func(s *CursorScope) { s.Principal = "other" },
		func(s *CursorScope) { s.Binding = "other" }, func(s *CursorScope) { s.Operation = core.Watch },
		func(s *CursorScope) { s.Target.Name = "#other" }, func(s *CursorScope) { s.Target.Kind = "direct"; s.Target.Name = "other" },
	} {
		changed := scope
		change(&changed)
		if _, err := c.Open(changed, token); err == nil {
			t.Fatal("cursor scope bypass", changed)
		}
	}
	bytes, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "gc1."))
	if err != nil {
		t.Fatal(err)
	}
	bytes[len(bytes)-1] ^= 1
	for _, invalid := range []string{position, "*", "../state", "gc2." + strings.TrimPrefix(token, "gc1."), token + "=", token + "x", token + "\n", strings.Repeat("x", 257), "gc1." + base64.RawURLEncoding.EncodeToString(bytes)} {
		if _, err := c.Open(scope, invalid); err == nil {
			t.Fatal("invalid cursor accepted", invalid)
		}
	}
	rotatedPath := filepath.Join(root, "rotated.key")
	if err := CreateCredential(rotatedPath); err != nil {
		t.Fatal(err)
	}
	rotated, err := NewCursorCodec(rotatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Open(scope, token); err == nil {
		t.Fatal("rotated key accepted old cursor")
	}
}

func TestCursorAEADGoLibraryKnownVector(t *testing.T) {
	// AES-256 empty-plaintext vector from Go crypto/cipher/gcm_test.go.
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "vector.key")
	if err := os.WriteFile(path, []byte("5394e890d37ba55ec9d5f327f15680f6a63ef5279c79331643ad0af6d2623525"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCursorCodec(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := hex.DecodeString("3c819d9a9bed087615030b65d9b260d4bc4630733ffb642f5ce45726")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.aead.Open(nil, nil, sealed, nil)
	if err != nil || len(plain) != 0 {
		t.Fatal("known library AEAD vector failed", err)
	}
}
