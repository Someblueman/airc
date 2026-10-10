package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type boundaryMaterial struct {
	signing   ed25519.PrivateKey
	recipient *ecdsa.PrivateKey
	scope     ResponseScope
	now       time.Time
}

func newBoundaryMaterial(t *testing.T) boundaryMaterial {
	t.Helper()
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return boundaryMaterial{signing, recipient, ResponseScope{"realm", "principal", "binding", "route"}, time.Unix(1000, 0)}
}

func boundaryToken(t *testing.T, m boundaryMaterial, envelope func(map[string]json.RawMessage)) string {
	return boundaryTokenWithHeaders(t, m, envelope, (&jose.SignerOptions{}).WithHeader("kid", "signer").WithType("airc-gateway-response+jws"), (&jose.EncrypterOptions{}).WithType("airc-gateway-response+jwe"))
}

func boundaryTokenWithHeaders(t *testing.T, m boundaryMaterial, envelope func(map[string]json.RawMessage), signing *jose.SignerOptions, encryption *jose.EncrypterOptions) string {
	t.Helper()
	sealer, err := NewResponseSealer("signer", m.signing)
	if err != nil {
		t.Fatal(err)
	}
	token, err := sealer.Seal(m.scope, "recipient", &m.recipient.PublicKey, []byte(`{"ok":true}`), m.now)
	if err != nil {
		t.Fatal(err)
	}
	jwe, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.ECDH_ES}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := jwe.Decrypt(m.recipient)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := jose.ParseSignedCompact(string(inner), []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := jws.Verify(m.signing.Public())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &fields); err != nil {
		t.Fatal(err)
	}
	envelope(fields)
	modified, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: m.signing}, signing)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(modified)
	if err != nil {
		t.Fatal(err)
	}
	innerToken, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &m.recipient.PublicKey, KeyID: "recipient"}, encryption)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := encrypter.Encrypt([]byte(innerToken))
	if err != nil {
		t.Fatal(err)
	}
	out, err := sealed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestResponseEnvelopePresenceAndScopeBoundaries(t *testing.T) {
	m := newBoundaryMaterial(t)
	m.now = time.Unix(0, 0)
	for _, field := range []string{"issued_at", "expires_at"} {
		t.Run("missing_"+field, func(t *testing.T) {
			token := boundaryToken(t, m, func(f map[string]json.RawMessage) { delete(f, field) })
			if _, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("error = %v", err)
			}
		})
		t.Run("null_"+field, func(t *testing.T) {
			token := boundaryToken(t, m, func(f map[string]json.RawMessage) { f[field] = json.RawMessage("null") })
			if _, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	for _, field := range []string{"realm", "principal", "binding", "route"} {
		t.Run("scope_"+field, func(t *testing.T) {
			token := boundaryToken(t, m, func(f map[string]json.RawMessage) {
				var scope map[string]string
				_ = json.Unmarshal(f["scope"], &scope)
				scope[field] = "other"
				f["scope"], _ = json.Marshal(scope)
			})
			if _, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestResponsePayloadExactCapacity(t *testing.T) {
	m := newBoundaryMaterial(t)
	sealer, err := NewResponseSealer("signer", m.signing)
	if err != nil {
		t.Fatal(err)
	}
	exact := []byte(`{"x":"` + strings.Repeat("a", MaxResponsePayloadBytes-8) + `"}`)
	if len(exact) != MaxResponsePayloadBytes {
		t.Fatalf("exact payload length = %d", len(exact))
	}
	token, err := sealer.Seal(m.scope, "recipient", &m.recipient.PublicKey, exact, m.now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now)
	if err != nil || string(got) != string(exact) {
		t.Fatalf("exact payload roundtrip: bytes=%d err=%v", len(got), err)
	}
	tooLarge := []byte(`{"x":"` + strings.Repeat("a", MaxResponsePayloadBytes-7) + `"}`)
	if _, err := sealer.Seal(m.scope, "recipient", &m.recipient.PublicKey, tooLarge, m.now); !errors.Is(err, ErrResponseCapacity) {
		t.Fatalf("over-capacity error = %v", err)
	}
}
