package auth

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	jose "github.com/go-jose/go-jose/v4"
	"testing"
	"time"
)

func openBoundary(t *testing.T, m boundaryMaterial, issued, expires, now int64) error {
	t.Helper()
	token := boundaryToken(t, m, func(f map[string]json.RawMessage) {
		f["issued_at"] = json.RawMessage(fmt.Sprintf("%d", issued))
		f["expires_at"] = json.RawMessage(fmt.Sprintf("%d", expires))
	})
	_, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, time.Unix(now, 0))
	return err
}

func TestResponseTimeWindows(t *testing.T) {
	m := newBoundaryMaterial(t)
	if err := openBoundary(t, m, 880, 1000, 1060); err != nil {
		t.Fatalf("expiry at skew boundary rejected: %v", err)
	}
	if err := openBoundary(t, m, 880, 1000, 1061); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expiry beyond skew error = %v", err)
	}
	if err := openBoundary(t, m, 1060, 1180, 1000); err != nil {
		t.Fatalf("issuance at skew boundary rejected: %v", err)
	}
	if err := openBoundary(t, m, 1061, 1181, 1000); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("future issuance beyond skew error = %v", err)
	}
	if err := openBoundary(t, m, 0, 120, 60); err != nil {
		t.Fatalf("epoch timestamps rejected: %v", err)
	}
	if err := openBoundary(t, m, -1, 119, 60); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("negative issuance error = %v", err)
	}
	if err := openBoundary(t, m, 0, 121, 60); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("overlong lifetime error = %v", err)
	}
}

func TestResponseHeaderRejectsWrongTypesAndUnknownFields(t *testing.T) {
	m := newBoundaryMaterial(t)
	for _, name := range []string{"inner_type", "inner_unknown", "outer_type", "outer_unknown"} {
		t.Run(name, func(t *testing.T) {
			signing := (&jose.SignerOptions{}).WithHeader("kid", "signer").WithType("airc-gateway-response+jws")
			encryption := (&jose.EncrypterOptions{}).WithType("airc-gateway-response+jwe")
			switch name {
			case "inner_type":
				signing.WithType("wrong")
			case "inner_unknown":
				signing.WithHeader("unknown", "value")
			case "outer_type":
				encryption.WithType("wrong")
			case "outer_unknown":
				encryption.WithHeader("unknown", "value")
			}
			token := boundaryTokenWithHeaders(t, m, func(map[string]json.RawMessage) {}, signing, encryption)
			// Authenticate both layers independently so rejection cannot merely be a
			// damaged ciphertext tag or signature.
			encrypted, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.ECDH_ES}, []jose.ContentEncryption{jose.A256GCM})
			if err != nil {
				t.Fatal(err)
			}
			inner, err := encrypted.Decrypt(m.recipient)
			if err != nil {
				t.Fatal(err)
			}
			signed, err := jose.ParseSignedCompact(string(inner), []jose.SignatureAlgorithm{jose.EdDSA})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := signed.Verify(m.signing.Public()); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("header accepted: %v", err)
			}
		})
	}
}
