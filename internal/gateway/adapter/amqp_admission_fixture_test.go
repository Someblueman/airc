//go:build darwin || linux

package adapter

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testfs"
	jose "github.com/go-jose/go-jose/v4"
)

type amqpAdmissionFixture struct {
	c        config.Config
	v        *auth.Verifier
	s        *store.Store
	d        *dispatch.Dispatcher
	identity auth.Identity
	signer   jose.Signer
	reader   *backend.Backend
	cursors  *auth.CursorCodec
}

func newAMQPAdmissionFixture(t *testing.T) amqpAdmissionFixture {
	t.Helper()
	root, err := testfs.MkdirTemp("amqp-admit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "amqp", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	secret := func(name string) string {
		path := filepath.Join(root, name)
		if err := auth.CreateCredential(path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c.CursorKeyFile = secret("cursor")
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: secret("account")}}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(jose.JSONWebKey{Key: pub})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "signer.json")
	if err := os.WriteFile(path, public, 0644); err != nil {
		t.Fatal(err)
	}
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	c.Principals = []config.Principal{{ID: "producer", Binding: "native", BearerTokenFile: secret("bearer"), SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: path}}, Operations: append(append([]core.Operation{}, ops...), core.ReceiptGet), Targets: []core.Target{{Kind: "room", Name: "#fixture"}, {Kind: "room", Name: "#other"}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	reader, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dispatch.New(s, v, reader, cursors, ops)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "signer"))
	if err != nil {
		t.Fatal(err)
	}
	return amqpAdmissionFixture{c: c, v: v, s: s, d: d, identity: identity, signer: signer, reader: reader, cursors: cursors}
}
func amqpRequest() core.Request {
	return core.Request{Version: 1, RequestID: "amqp-stable-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

// incoming wraps a plain contract request, exactly as an HTTP client would
// send it, in one complete AMQP data-section delivery.
func (f amqpAdmissionFixture) incoming(t *testing.T, r core.Request) *amqpIncoming {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return amqpIncomingBody(t, body)
}
func amqpIncomingBody(t *testing.T, body []byte) *amqpIncoming {
	t.Helper()
	wire, err := amqpwire.EncodeDataMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	return &amqpIncoming{id: 42, tag: []byte("wire"), encoded: wire, complete: true, deadline: time.Now().Add(amqpMessageDeadline)}
}
func (f amqpAdmissionFixture) route() config.AMQPRoute {
	return config.AMQPRoute{Principal: f.identity.ID(), RequestAddress: "request", ResponseAddress: "response", Target: *amqpRequest().Target, Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}
}
