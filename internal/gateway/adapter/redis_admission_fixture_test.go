//go:build darwin || linux

package adapter

import (
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

// redisTestFixture holds a Redis route's dispatcher and a guest author
// policy: requests name their authors, as on every carrier.
type redisTestFixture struct {
	c       config.Config
	s       *store.Store
	d       *dispatch.Dispatcher
	author  dispatch.RouteAuthor
	reader  *backend.Backend
	cursors *auth.CursorCodec
}

// redisTestAuthor is the guest every fixture request names.
const redisTestAuthor = "redis-guest"

// lookup reads a job of the fixture's guest author.
func (f redisTestFixture) lookup(t *testing.T, requestID string) (store.Job, error) {
	t.Helper()
	return f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: redisTestAuthor}}, requestID)
}

// authors maps each route of b to the fixture's author policy.
func (f redisTestFixture) authors(b config.RedisPubSub) map[string]dispatch.RouteAuthor {
	authors := make(map[string]dispatch.RouteAuthor, len(b.Routes))
	for _, r := range b.Routes {
		authors[r.RequestChannel] = f.author
	}
	return authors
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
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	route := redisAdmissionRoute()
	c.RedisPubSub = []config.RedisPubSub{{ID: "redis", Address: "127.0.0.1:6379", TLSServerName: "localhost",
		Subscriber: config.RedisCredential{Username: "reader", PasswordFile: secret("reader")}, Publisher: config.RedisCredential{Username: "writer", PasswordFile: secret("writer")},
		Routes: []config.RedisRoute{route}}}
	f := redisTestFixture{c: c}
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
	if f.d, err = dispatch.ForRoute(f.s, f.reader, f.cursors, "redis", redisTestOperations); err != nil {
		t.Fatal(err)
	}
	if f.author, err = dispatch.NewRouteAuthor(nil, true); err != nil {
		t.Fatal(err)
	}
	return f
}

func redisAdmissionRoute() config.RedisRoute {
	return config.RedisRoute{RequestChannel: "airc request.*é", ResponseChannel: "airc response", Target: core.Target{Kind: "room", Name: "#fixture"}, Operations: redisTestOperations}
}

func redisRequest() core.Request {
	return core.Request{Version: 1, RequestID: "redis-stable-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

// redisPlainRequest is the contract-v1 JSON body an HTTP client would send,
// naming the fixture's guest author.
func redisPlainRequest(t *testing.T, r core.Request) []byte {
	t.Helper()
	return authorRequestJSON(t, r, redisTestAuthor)
}
