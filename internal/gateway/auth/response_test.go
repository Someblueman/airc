package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

func responseKeys(t *testing.T) (ed25519.PrivateKey, *ecdsa.PrivateKey) {
	t.Helper()
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signing, recipient
}

func TestResponseSealOpenAndScope(t *testing.T) {
	signing, recipient := responseKeys(t)
	sealer, err := NewResponseSealer("gateway", signing)
	if err != nil {
		t.Fatal(err)
	}
	scope := ResponseScope{Realm: "airc", Principal: "agent", Binding: "binding", Route: "udp"}
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"operation":"send","request_id":"r1"}`)
	token, err := sealer.Seal(scope, "recipient", &recipient.PublicKey, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	jwe, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.ECDH_ES}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil || jwe.Header.Algorithm != "ECDH-ES" || jwe.Header.KeyID != "recipient" || jwe.Header.ExtraHeaders[jose.HeaderType] != "airc-gateway-response+jwe" {
		t.Fatalf("independent JWE verification failed: %v %+v", err, jwe.Header)
	}
	inner, err := jwe.Decrypt(recipient)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := jose.ParseSignedCompact(string(inner), []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || jws.Signatures[0].Header.KeyID != "gateway" || jws.Signatures[0].Header.ExtraHeaders[jose.HeaderType] != "airc-gateway-response+jws" {
		t.Fatalf("independent JWS verification failed: %v", err)
	}
	if _, err := jws.Verify(signing.Public()); err != nil {
		t.Fatal(err)
	}
	got, err := OpenResponse(token, "gateway", signing.Public().(ed25519.PublicKey), "recipient", recipient, scope, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %s, want %s", got, payload)
	}
	if _, err := OpenResponse(token, "gateway", signing.Public().(ed25519.PublicKey), "other", recipient, scope, now); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong recipient error = %v", err)
	}
	if _, err := OpenResponse(token, "gateway", signing.Public().(ed25519.PublicKey), "recipient", recipient, ResponseScope{Realm: "other", Principal: scope.Principal, Binding: scope.Binding, Route: scope.Route}, now); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong scope error = %v", err)
	}
	parts := strings.Split(token, ".")
	tag, decodeErr := base64.RawURLEncoding.DecodeString(parts[4])
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	tag[0] ^= 1
	parts[4] = base64.RawURLEncoding.EncodeToString(tag)
	if _, err := OpenResponse(strings.Join(parts, "."), "gateway", signing.Public().(ed25519.PublicKey), "recipient", recipient, scope, now); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered token error = %v", err)
	}
	if _, err := OpenResponse(token, "gateway", signing.Public().(ed25519.PublicKey), "recipient", recipient, scope, now.Add(181*time.Second)); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expired token error = %v", err)
	}
}

func TestResponseBoundsAndKeyValidation(t *testing.T) {
	signing, recipient := responseKeys(t)
	if _, err := NewResponseSealer("bad id", signing); !errors.Is(err, ErrCredential) {
		t.Fatalf("bad signing id error = %v", err)
	}
	sealer, _ := NewResponseSealer("gateway", signing)
	scope := ResponseScope{Realm: "airc", Principal: "agent", Binding: "binding", Route: "udp"}
	if _, err := sealer.Seal(scope, "recipient", &recipient.PublicKey, []byte(`[]`), time.Now()); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("array payload error = %v", err)
	}
	if _, err := sealer.Seal(scope, "recipient", &recipient.PublicKey, []byte(`{"x":"`+string(make([]byte, MaxResponsePayloadBytes))+`"}`), time.Now()); !errors.Is(err, ErrResponseCapacity) {
		t.Fatalf("oversized payload error = %v", err)
	}
}

func TestResponseSealerConcurrentUse(t *testing.T) {
	signing, recipient := responseKeys(t)
	sealer, err := NewResponseSealer("gateway", signing)
	if err != nil {
		t.Fatal(err)
	}
	scope := ResponseScope{Realm: "airc", Principal: "agent", Binding: "binding", Route: "udp"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, sealErr := sealer.Seal(scope, "recipient", &recipient.PublicKey, []byte(`{"ok":true}`), time.Unix(1_700_000_000, 0))
			if sealErr != nil {
				t.Errorf("seal: %v", sealErr)
				return
			}
			if _, openErr := OpenResponse(token, "gateway", signing.Public().(ed25519.PublicKey), "recipient", recipient, scope, time.Unix(1_700_000_001, 0)); openErr != nil {
				t.Errorf("open: %v", openErr)
			}
		}()
	}
	wg.Wait()
}
