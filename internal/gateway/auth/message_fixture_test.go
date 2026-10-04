//go:build unix

package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	jose "github.com/go-jose/go-jose/v4"
)

func messageFixture(t *testing.T) (config.Config, ed25519.PrivateKey, *ecdsa.PrivateKey) {
	t.Helper()
	c, _ := fixtureConfig(t)
	root := filepath.Dir(c.Principals[0].BearerTokenFile)
	pub, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(publicJWK{Type: "OKP", Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)})
	publicPath := filepath.Join(root, "signing.json")
	if err := os.WriteFile(publicPath, public, 0644); err != nil {
		t.Fatal(err)
	}
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].SigningKeys = []config.SigningKey{{ID: "signer", PublicKeyFile: publicPath}}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(recipient)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "recipient.pem")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	c.RecipientKeys = []config.RecipientKey{{ID: "recipient", PrivateKeyFile: privatePath}}
	return c, signing, recipient
}

func intent() MessageEnvelope {
	i, e := int64(1000), int64(1100)
	return MessageEnvelope{Version: 1, Audience: "fixture", IssuedAt: i, ExpiresAt: e, Request: core.Request{Version: 1, RequestID: "message", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Message: "first\r\nsecond", IssuedAt: &i, ExpiresAt: &e}}
}

func signBytes(t testing.TB, key ed25519.PrivateKey, payload []byte) string {
	t.Helper()
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "signer"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := o.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func signIntent(t testing.TB, key ed25519.PrivateKey, envelope MessageEnvelope) string {
	t.Helper()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return signBytes(t, key, payload)
}

func encryptBytes(t testing.TB, key *ecdsa.PrivateKey, payload string) string {
	t.Helper()
	e, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &key.PublicKey, KeyID: "recipient"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	o, err := e.Encrypt([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	token, err := o.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
