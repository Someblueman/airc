//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testfs"
)

// redisTestFixture holds a dispatcher whose only principal has no credential:
// a disabled Redis route posts as it, as an owner would configure.
type redisTestFixture struct {
	c        config.Config
	v        *auth.Verifier
	s        *store.Store
	d        *dispatch.Dispatcher
	identity auth.Identity
	reader   *backend.Backend
	cursors  *auth.CursorCodec
}

var redisTestOperations = []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}

func newRedisFixture(t *testing.T) redisTestFixture {
	t.Helper()
	root, err := testfs.MkdirTemp("redis-admit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "redis", filepath.Join(root, "state")
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
	c.Principals = []config.Principal{{ID: "producer", Binding: "native", Operations: append(append([]core.Operation{}, redisTestOperations...), core.ReceiptGet), Targets: []core.Target{{Kind: "room", Name: "#fixture"}, {Kind: "room", Name: "#other"}}}}
	route := redisAdmissionRoute()
	c.RedisPubSub = []config.RedisPubSub{{ID: "redis", Address: "127.0.0.1:6379", TLSServerName: "localhost",
		Subscriber: config.RedisCredential{Username: "reader", PasswordFile: secret("reader")}, Publisher: config.RedisCredential{Username: "writer", PasswordFile: secret("writer")},
		Routes: []config.RedisRoute{route}}}
	f := redisTestFixture{c: c}
	if f.v, err = auth.New(c); err != nil {
		t.Fatal(err)
	}
	if f.s, err = store.Open(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.Close() })
	if f.reader, err = backend.New(c); err != nil {
		t.Fatal(err)
	}
	if f.cursors, err = auth.NewCursorCodec(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	if f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, redisTestOperations); err != nil {
		t.Fatal(err)
	}
	if f.identity, err = f.v.Route("producer"); err != nil {
		t.Fatal(err)
	}
	return f
}

func redisAdmissionRoute() config.RedisRoute {
	return config.RedisRoute{Principal: "producer", RequestChannel: "airc request.*é", ResponseChannel: "airc response", Target: core.Target{Kind: "room", Name: "#fixture"}, Operations: redisTestOperations}
}

func redisRequest() core.Request {
	return core.Request{Version: 1, RequestID: "redis-stable-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

// redisPlainRequest is the contract-v1 JSON body an HTTP client would send.
func redisPlainRequest(t *testing.T, r core.Request) []byte {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
