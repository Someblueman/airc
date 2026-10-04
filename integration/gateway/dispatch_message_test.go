package gateway_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	jose "github.com/go-jose/go-jose/v4"
)

func TestDispatchAuthenticatedProfilesCoalesceAndDurableRevocation(t *testing.T) {
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, _ := acceptanceJournalFixture(t, socket, account)
	root := filepath.Dir(c.Principals[0].BearerTokenFile)
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(struct {
		Type  string `json:"kty"`
		Curve string `json:"crv"`
		X     string `json:"x"`
	}{"OKP", "Ed25519", base64.RawURLEncoding.EncodeToString(pub)})
	if err != nil {
		t.Fatal(err)
	}
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
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := dispatch.New(s, v, []core.Operation{core.Send, core.Reply, core.ReceiptGet})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	i, e := now.Unix(), now.Unix()+30
	r := core.Request{Version: 1, RequestID: "profiles", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "same authenticated intent", IssuedAt: &i, ExpiresAt: &e}
	payload, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: c.Realm, IssuedAt: i, ExpiresAt: e, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: private}, (&jose.SignerOptions{}).WithHeader("kid", "signer"))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &recipient.PublicKey, KeyID: "recipient"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encrypter.Encrypt([]byte(jws))
	if err != nil {
		t.Fatal(err)
	}
	jwe, err := encrypted.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, call := range []func() (core.Result, error){
		func() (core.Result, error) { return d.Signed(ctx, jws, now) },
		func() (core.Result, error) { return d.Encrypted(ctx, jwe, now) },
		func() (core.Result, error) { return d.Session(ctx, token, requestJSON(t, r), now) },
	} {
		result, err := call()
		if err != nil || !result.Durable || result.RequestID != r.RequestID {
			t.Fatal("authenticated profile admission failed", result, err)
		}
	}
	identity := fixtureIdentity(t, c)
	j, err := s.Lookup(ctx, identity, r.RequestID)
	if err != nil || j.ReplayUntil != e+auth.ClockSkewSeconds || j.Result.SendAttempts != 0 {
		t.Fatal("profiles failed to coalesce replay marker", j, err)
	}
	if _, err := s.Revoke(ctx, identity.ID()); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() (core.Result, error){
		func() (core.Result, error) { return d.Signed(ctx, jws, now) },
		func() (core.Result, error) { return d.Encrypted(ctx, jwe, now) },
		func() (core.Result, error) { return d.Session(ctx, token, requestJSON(t, r), now) },
	} {
		if _, err := call(); !errors.Is(err, auth.ErrForbidden) {
			t.Fatal("profile bypassed durable revocation", err)
		}
	}
}
