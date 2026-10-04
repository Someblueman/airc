package gateway_test

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
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	jose "github.com/go-jose/go-jose/v4"
)

type detachedCredentials struct {
	realm               string
	signer              jose.Signer
	encrypter           jose.Encrypter
	browserSigningKey   json.RawMessage
	browserRecipientKey json.RawMessage
}

func provisionDetached(t *testing.T, c *config.Config) *detachedCredentials {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(c.Principals[0].BearerTokenFile)
	publicPath := filepath.Join(root, "signing.json")
	if err := os.WriteFile(publicPath, public, 0644); err != nil {
		t.Fatal(err)
	}
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
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: private}, (&jose.SignerOptions{}).WithHeader("kid", "signer"))
	if err != nil {
		t.Fatal(err)
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &recipient.PublicKey, KeyID: "recipient"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	privateJWK, err := json.Marshal(jose.JSONWebKey{Key: private})
	if err != nil {
		t.Fatal(err)
	}
	publicJWK, err := json.Marshal(jose.JSONWebKey{Key: &recipient.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	return &detachedCredentials{realm: c.Realm, signer: signer, encrypter: encrypter, browserSigningKey: privateJWK, browserRecipientKey: publicJWK}
}

func (c *detachedCredentials) token(t *testing.T, r core.Request, encrypted bool) string {
	t.Helper()
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	if r.IssuedAt != nil {
		issued = *r.IssuedAt
	}
	if r.ExpiresAt != nil {
		expiry = *r.ExpiresAt
	}
	return c.validityToken(t, r, issued, expiry, encrypted)
}

func (c *detachedCredentials) validityToken(t *testing.T, r core.Request, issued, expiry int64, encrypted bool) string {
	t.Helper()
	if r.Operation == core.Send || r.Operation == core.Reply || r.Operation == core.ReactionAdd || r.Operation == core.ProfileUpdate || r.Operation == core.PresenceSet || r.Operation == core.TopicSet {
		r.IssuedAt = &issued
		r.ExpiresAt = &expiry
	}
	payload, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: c.realm, IssuedAt: issued, ExpiresAt: expiry, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := c.signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	if !encrypted {
		return token
	}
	object, err := c.encrypter.Encrypt([]byte(token))
	if err != nil {
		t.Fatal(err)
	}
	token, err = object.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
