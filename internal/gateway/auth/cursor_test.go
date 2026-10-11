//go:build darwin || linux

package auth

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
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
	scope := CursorScope{Realm: "realm", Author: "a:alice", Operation: core.HistoryRead, Target: core.Target{Kind: "room", Name: "#room"}}
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
		func(s *CursorScope) { s.Realm = "other" }, func(s *CursorScope) { s.Author = "a:bob" },
		func(s *CursorScope) { s.Author = "g:alice" }, func(s *CursorScope) { s.Operation = core.Watch },
		func(s *CursorScope) { s.Target.Name = "#other" }, func(s *CursorScope) { s.Target.Kind = "direct"; s.Target.Name = "other" },
		func(s *CursorScope) { s.Query = "different query" },
		func(s *CursorScope) { s.From = "other" },
		func(s *CursorScope) { s.ParentID = strings.Repeat("b", 32) },
		func(s *CursorScope) { s.ThreadMode = "thread" },
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

func TestHistoryCursorAADRemainsCompatible(t *testing.T) {
	scope := CursorScope{Realm: "realm", Author: "a:alice", Operation: core.HistoryRead, Target: core.Target{Kind: "room", Name: "#room"}}
	aad, err := cursorAAD(scope)
	const expected = "airc-gateway-cursor-v2\x00" + `{"Realm":"realm","Author":"a:alice","Operation":"history.read","Target":{"kind":"room","name":"#room"}}`
	if err != nil || string(aad) != expected {
		t.Fatal("published history cursor AAD changed", string(aad), err)
	}
	withEmptyFields := scope
	withEmptyFields.ParentID, withEmptyFields.ThreadMode = "", ""
	legacyAAD, err := cursorAAD(withEmptyFields)
	if err != nil || string(legacyAAD) != string(aad) {
		t.Fatal("omitted conversation fields changed ordinary cursor AAD")
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

// A cursor belongs to (realm, author scope, query), never to the listener or
// transport that issued it, so a client falling back to another carrier can
// resume history where it left off.
func TestCursorResumesAcrossListeners(t *testing.T) {
	allowed := map[string]bool{"Realm": true, "Author": true, "Operation": true, "Target": true, "Query": true, "From": true, "ParentID": true, "ThreadMode": true}
	scopeType := reflect.TypeFor[CursorScope]()
	for i := range scopeType.NumField() {
		if name := scopeType.Field(i).Name; !allowed[name] {
			t.Fatal("cursor scope gained a non-author field", name)
		}
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cursor.key")
	if err := CreateCredential(path); err != nil {
		t.Fatal(err)
	}
	codec, err := NewCursorCodec(path)
	if err != nil {
		t.Fatal(err)
	}
	position := strings.Repeat("c", 32)
	// Listener "http" seals; listener "mqtt" rebuilds the scope from the
	// same author and request alone and opens it.
	issued := CursorScope{Realm: "realm", Author: core.Author{Nick: "Alice"}.Scope(), Operation: core.HistoryRead, Target: core.Target{Kind: "room", Name: "#room"}}
	token, err := codec.Seal(issued, position)
	if err != nil {
		t.Fatal(err)
	}
	resumed := CursorScope{Realm: "realm", Author: core.Author{Nick: "alice"}.Scope(), Operation: core.HistoryRead, Target: core.Target{Kind: "room", Name: "#room"}}
	if after, err := codec.Open(resumed, token); err != nil || after != position {
		t.Fatal("cursor did not resume on another listener", after, err)
	}
	for _, other := range []string{core.Author{Nick: "alice", Registered: true}.Scope(), core.Author{Nick: "bob"}.Scope()} {
		changed := resumed
		changed.Author = other
		if _, err := codec.Open(changed, token); err == nil {
			t.Fatal("cursor opened for another author", other)
		}
	}
	for _, invalid := range []string{"", "alice", "g:Alice"} {
		changed := issued
		changed.Author = invalid
		if _, err := codec.Seal(changed, position); err == nil {
			t.Fatal("sealed for invalid author scope", invalid)
		}
	}
}
