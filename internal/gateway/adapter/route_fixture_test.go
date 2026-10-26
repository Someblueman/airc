package adapter

import (
	"database/sql"
	"net/url"
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

// authorTestToken is a well-formed account token. The fixture never dials
// AIRC, so it is only ever hashed into the journal.
const authorTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// routeFixture is a real journal, an author upstream that is never dialled and
// a cursor codec, for dispatchers of author carriers: requests name their
// authors.
type routeFixture struct {
	c        config.Config
	s        *store.Store
	reader   *backend.Backend
	cursors  *auth.CursorCodec
	root     string
	stateDir string
}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	// A short root keeps the native socket path within Darwin's limit.
	root, err := testfs.MkdirTemp("route-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "plain-route", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "unused.sock")}
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
	return &routeFixture{c: c, s: s, reader: reader, cursors: cursors, root: root, stateDir: c.StateDir}
}

// listener returns l's dispatcher. A PasswordFile holds the password itself
// and is written to a private file here.
func (f *routeFixture) listener(t *testing.T, l config.Listener) *dispatch.Dispatcher {
	t.Helper()
	if l.PasswordFile != "" {
		path := filepath.Join(f.root, l.ID+".password")
		if err := os.WriteFile(path, []byte(l.PasswordFile+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		l.PasswordFile = path
	}
	d, err := dispatch.ForListener(f.s, f.reader, f.cursors, l)
	if err != nil || !d.AuthorAccess() {
		t.Fatal("listener dispatcher", err)
	}
	return d
}

// jobs counts journal rows for a request ID under every author scope, so a
// refusal cannot hide intake attributed to someone else.
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

// owner returns the author of a journalled guest or registered job.
func (f *routeFixture) owner(t *testing.T, nick string, registered bool, requestID string) core.Author {
	t.Helper()
	sub := store.Submission{Author: core.Author{Nick: nick, Registered: registered}}
	if registered {
		sub.Credential, _ = core.NewCredential(authorTestToken)
	}
	j, err := f.s.LookupAuthor(t.Context(), sub, requestID)
	if err != nil {
		t.Fatalf("job %s for %s: %v", requestID, nick, err)
	}
	return j.Author
}

const plainRouteSend = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"first\nsecond 🙂","author":{"nick":"route-guest"}}`
