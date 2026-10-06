package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	jose "github.com/go-jose/go-jose/v4"
	"strings"
	"testing"
	"time"
)

func TestSignedResponsePreservesEnvelopeAndExactScope(t *testing.T) {
	m := newBoundaryMaterial(t)
	sealer, err := NewResponseSealer("signer", m.signing)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"exact":"line\n世界"}`)
	token, err := sealer.Sign(m.scope, payload, m.now)
	if err != nil {
		t.Fatal(err)
	}
	// Verify the literal compact signing input with the standard Ed25519
	// verifier before exercising the gateway's scoped response reader.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("not a compact JWS")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(m.signing.Public().(ed25519.PublicKey), []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("invalid signature")
	}
	plain, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version int             `json:"version"`
		Purpose string          `json:"purpose"`
		Scope   ResponseScope   `json:"scope"`
		Payload json.RawMessage `json:"payload"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &fields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(plain, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(fields["issued_at"]) != "1000" || string(fields["expires_at"]) != "1120" || envelope.Version != 1 || envelope.Purpose != "airc-gateway-response" || envelope.Scope != m.scope || string(envelope.Payload) != string(payload) {
		t.Fatal(string(plain))
	}
	got, err := OpenSignedResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), m.scope, m.now)
	if err != nil || string(got) != string(payload) {
		t.Fatal(string(got), err)
	}
	for _, scope := range []ResponseScope{{"other", m.scope.Principal, m.scope.Binding, m.scope.Route}, {m.scope.Realm, "other", m.scope.Binding, m.scope.Route}, {m.scope.Realm, m.scope.Principal, "other", m.scope.Route}, {m.scope.Realm, m.scope.Principal, m.scope.Binding, "other"}} {
		if _, err := OpenSignedResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), scope, m.now); !errors.Is(err, ErrAuthentication) {
			t.Fatal(scope, err)
		}
	}
	for _, now := range []time.Time{time.Unix(939, 0), time.Unix(1181, 0)} {
		if _, err := OpenSignedResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), m.scope, now); !errors.Is(err, ErrAuthentication) {
			t.Fatal(now, err)
		}
	}
	// The existing encrypted profile still demands a JWE; no fallback to this
	// new signing-only carrier is accepted by its receiver.
	if _, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
		t.Fatal(err)
	}
}
func TestSignedResponseRejectsWrongTypeAndInvalidIssuer(t *testing.T) {
	m := newBoundaryMaterial(t)
	token := boundaryTokenWithHeaders(t, m, func(map[string]json.RawMessage) {}, (&jose.SignerOptions{}).WithHeader("kid", "signer").WithType("wrong"), (&jose.EncrypterOptions{}).WithType("airc-gateway-response+jwe"))
	sealed, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.ECDH_ES}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := sealed.Decrypt(m.recipient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSignedResponse(string(inner), "signer", m.signing.Public().(ed25519.PublicKey), m.scope, m.now); !errors.Is(err, ErrAuthentication) {
		t.Fatal(err)
	}
	var sealer *ResponseSealer
	if _, err := sealer.Sign(m.scope, []byte(`{}`), m.now); !errors.Is(err, ErrCredential) {
		t.Fatal(err)
	}
}
