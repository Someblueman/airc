package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

var ErrCursor = errors.New(string(core.InvalidRequest))

// CursorCodec snapshots a dedicated owner-provisioned 256-bit key. The standard
// AEAD owns random nonce generation and framing; no secrets enter journal state.
type CursorCodec struct{ aead cipher.AEAD }

type CursorScope struct {
	Realm, Principal, Binding string
	Operation                 core.Operation
	Target                    core.Target
	Query                     string `json:",omitempty"`
	From                      string `json:",omitempty"`
	ParentID                  string `json:",omitempty"`
	ThreadMode                string `json:",omitempty"`
}

func NewCursorCodec(path string) (*CursorCodec, error) {
	if path == "" {
		return nil, nil
	}
	token, err := ReadToken(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(token)
	if err != nil {
		return nil, ErrCredential
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCredential
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrCredential
	}
	return &CursorCodec{aead}, nil
}

func cursorAAD(scope CursorScope) ([]byte, error) {
	if scope.Realm == "" || scope.Principal == "" || scope.Binding == "" || !scope.Operation.Valid() {
		return nil, ErrCursor
	}
	target := scope.Target
	if err := target.Normalize(); err != nil || target != scope.Target {
		return nil, ErrCursor
	}
	data, err := json.Marshal(scope)
	if err != nil {
		return nil, ErrCursor
	}
	return append([]byte("airc-gateway-cursor-v1\x00"), data...), nil
}

func cursorPosition(after string) bool {
	if len(after) != 32 {
		return false
	}
	_, err := hex.DecodeString(after)
	return err == nil
}

func (c *CursorCodec) Seal(scope CursorScope, after string) (string, error) {
	if c == nil || !cursorPosition(after) {
		return "", ErrCursor
	}
	aad, err := cursorAAD(scope)
	if err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nil, []byte(after), aad)
	return "gc1." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *CursorCodec) Open(scope CursorScope, token string) (string, error) {
	if c == nil || len(token) > 256 || !strings.HasPrefix(token, "gc1.") {
		return "", ErrCursor
	}
	encoded := strings.TrimPrefix(token, "gc1.")
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sealed) != 32+c.aead.Overhead() || base64.RawURLEncoding.EncodeToString(sealed) != encoded {
		return "", ErrCursor
	}
	aad, err := cursorAAD(scope)
	if err != nil {
		return "", err
	}
	plain, err := c.aead.Open(nil, nil, sealed, aad)
	if err != nil || !cursorPosition(string(plain)) {
		return "", ErrCursor
	}
	return string(plain), nil
}
