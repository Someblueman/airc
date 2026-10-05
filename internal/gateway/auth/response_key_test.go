package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestResponseKeyValidationAndCopy(t *testing.T) {
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bad := append(ed25519.PrivateKey(nil), signing...)
	bad[ed25519.SeedSize] ^= 1
	if _, err := NewResponseSealer("signer", bad); !errors.Is(err, ErrCredential) {
		t.Fatalf("inconsistent key error = %v", err)
	}
	if _, err := NewResponseSealer("signer", ed25519.PrivateKey{}); !errors.Is(err, ErrCredential) {
		t.Fatalf("empty key error = %v", err)
	}
	var zero ResponseSealer
	var absent *ResponseSealer
	if _, err := absent.Seal(ResponseScope{"r", "p", "b", "route"}, "recipient", nil, []byte(`{"x":true}`), time.Unix(1000, 0)); !errors.Is(err, ErrCredential) {
		t.Fatalf("nil sealer error = %v", err)
	}
	if _, err := zero.Seal(ResponseScope{"r", "p", "b", "route"}, "recipient", nil, []byte(`{"x":true}`), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) && !errors.Is(err, ErrCredential) {
		t.Fatalf("zero sealer error = %v", err)
	}
	m := newBoundaryMaterial(t)
	sealer, err := NewResponseSealer("signer", m.signing)
	if err != nil {
		t.Fatal(err)
	}
	copyBefore := append(ed25519.PrivateKey(nil), m.signing...)
	for i := range m.signing {
		m.signing[i] ^= 0xff
	}
	token, err := sealer.Seal(m.scope, "recipient", &m.recipient.PublicKey, []byte(`{"copied":true}`), m.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenResponse(token, "signer", copyBefore.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now); err != nil {
		t.Fatal(err)
	}
	wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenResponse(token, "signer", wrongPub, "recipient", m.recipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong signing key error = %v", err)
	}
	wrongRecipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenResponse(token, "signer", copyBefore.Public().(ed25519.PublicKey), "recipient", wrongRecipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong recipient key error = %v", err)
	}
}
