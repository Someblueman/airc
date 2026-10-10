package adapter

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// routeFixture is a real journal and verifier for plain route-owned requests.
// "producer" may send to #fixture and #other; "retired" is revoked.
type routeFixture struct {
	d        *dispatch.Dispatcher
	s        *store.Store
	producer auth.Identity
	stateDir string
}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "plain-route", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, name := range []string{"account", "producer", "retired"} {
		paths[name] = filepath.Join(root, name)
		if err := auth.CreateCredential(paths[name]); err != nil {
			t.Fatal(err)
		}
	}
	targets := []core.Target{{Kind: "room", Name: "#fixture"}, {Kind: "room", Name: "#other"}}
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "unused.sock"), Account: "author", AccountTokenFile: paths["account"]}}
	c.Principals = []config.Principal{
		{ID: "producer", Binding: "native", BearerTokenFile: paths["producer"], Operations: []core.Operation{core.Send, core.ReceiptGet}, Targets: targets},
		{ID: "retired", Binding: "native", BearerTokenFile: paths["retired"], Revoked: true, Operations: []core.Operation{core.Send}, Targets: targets},
	}
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
	token, err := auth.ReadToken(paths["producer"])
	if err != nil {
		t.Fatal(err)
	}
	producer, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	return &routeFixture{d: d, s: s, producer: producer, stateDir: c.StateDir}
}

// jobs counts journal rows for a request ID under every principal, so a refusal
// cannot hide intake attributed to someone else.
func (f *routeFixture) jobs(t *testing.T, requestID string) int {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.stateDir, "journal.sqlite"), RawQuery: "mode=ro&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", requestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

const plainRouteSend = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"first\nsecond 🙂"}`
