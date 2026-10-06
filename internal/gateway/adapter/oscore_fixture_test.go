//go:build darwin || linux

package adapter

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

func oscoreRPCFixture(t *testing.T) (*dispatch.Dispatcher, *store.Store, auth.Identity, []byte, auth.OSCOREOwner) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "oscore-rpc", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	account := filepath.Join(root, "account")
	if err := auth.CreateCredential(account); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "unused.sock"), Account: "author", AccountTokenFile: account}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(root, "signing.json")
	if err := os.WriteFile(publicPath, key, 0644); err != nil {
		t.Fatal(err)
	}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend", SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: publicPath}}, Operations: []core.Operation{core.Send, core.ReceiptGet}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
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
	issued, expires := time.Now().Unix(), time.Now().Unix()+300
	envelope := auth.MessageEnvelope{Version: 1, Audience: c.Realm, IssuedAt: issued, ExpiresAt: expires, Request: core.Request{Version: 1, RequestID: "oscore-rpc-message", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "first\r\nsecond", IssuedAt: &issued, ExpiresAt: &expires}}
	payload, err := json.Marshal(envelope)
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
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	verified, err := v.AuthenticateSigned(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(FrameRequest{Version: 1, CorrelationID: "native-rpc", Profile: "signed", Envelope: token})
	if err != nil {
		t.Fatal(err)
	}
	return d, s, verified.Identity, body, auth.OSCOREOwner{Realm: c.Realm, Principal: "device", Binding: "backend"}
}
