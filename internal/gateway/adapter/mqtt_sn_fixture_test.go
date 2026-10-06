//go:build darwin || linux

package adapter

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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

type mqttSNFixture struct {
	d         *dispatch.Dispatcher
	s         *store.Store
	identity  auth.Identity
	signer    jose.Signer
	encrypter jose.Encrypter
}

func newMQTTSNFixture(t *testing.T) mqttSNFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mqtt-sn", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	account := filepath.Join(root, "account")
	if err := auth.CreateCredential(account); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "unused.sock"), Account: "author", AccountTokenFile: account}}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(jose.JSONWebKey{Key: pub})
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(root, "signer.json")
	if err := os.WriteFile(publicPath, public, 0644); err != nil {
		t.Fatal(err)
	}
	c.Principals = []config.Principal{{ID: "sensor", Binding: "native", SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: publicPath}}, Operations: []core.Operation{core.Send, core.ReceiptGet}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
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
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	d, err := dispatch.New(s, v, nil, nil, []core.Operation{core.Send})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "signer"))
	if err != nil {
		t.Fatal(err)
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &recipient.PublicKey, KeyID: "recipient"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := mqttSNFixture{d: d, s: s, signer: signer, encrypter: encrypter}
	message, err := v.AuthenticateEncrypted(f.token(t, mqttSNRequest(), true, 0), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.identity = message.Identity
	return f
}

func mqttSNRequest() core.Request {
	return core.Request{Version: 1, RequestID: "mqtt-sn-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

func (f mqttSNFixture) token(t *testing.T, request core.Request, encrypted bool, shift int64) string {
	t.Helper()
	issued, expires := time.Now().Unix()+shift, time.Now().Unix()+shift+300
	if request.Operation == core.Send {
		request.IssuedAt, request.ExpiresAt = &issued, &expires
	}
	body, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: "mqtt-sn", IssuedAt: issued, ExpiresAt: expires, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.signer.Sign(body)
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
	sealed, err := f.encrypter.Encrypt([]byte(token))
	if err != nil {
		t.Fatal(err)
	}
	token, err = sealed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
