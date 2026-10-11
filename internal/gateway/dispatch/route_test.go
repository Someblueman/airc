package dispatch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// routeFixture is a route-owned ingress (a broker route) with no listener.
func routeFixture(t *testing.T, ops []core.Operation) (*Dispatcher, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "route", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "airc.sock")}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	d, err := ForRoute(s, b, nil, "redis", ops)
	if err != nil {
		t.Fatal(err)
	}
	return d, s, root
}

func routeBody(t *testing.T, id string, author map[string]string) []byte {
	t.Helper()
	request := map[string]any{"version": 1, "request_id": id, "operation": "send", "target": map[string]string{"kind": "room", "name": "#ops"}, "message": "hello"}
	if author != nil {
		request["author"] = author
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestRouteAuthorPolicy(t *testing.T) {
	d, s, root := routeFixture(t, []core.Operation{core.Send})
	if !d.AuthorAccess() || d.ListenerID() != "redis" || RouteSource("redis") != "route:redis" {
		t.Fatal("route dispatcher has no gate")
	}
	tokenFile := filepath.Join(root, "bot.token")
	if err := os.WriteFile(tokenFile, []byte(gateToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bound, err := NewRouteAuthor(&config.BoundAuthor{Nick: "alerts", AccountTokenFile: tokenFile}, false)
	if err != nil {
		t.Fatal(err)
	}
	guests, err := NewRouteAuthor(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRouteAuthor(nil, false); !errors.Is(err, config.ErrRouteAuthor) {
		t.Fatal("route without bound author or guests", err)
	}
	if _, err := NewRouteAuthor(&config.BoundAuthor{Nick: "alerts", AccountTokenFile: filepath.Join(root, "missing")}, false); err == nil {
		t.Fatal("unreadable bound token accepted")
	}
	now := time.Unix(1000, 0)
	source := RouteSource("redis")
	lookup := func(sub store.Submission, id string) core.Author {
		t.Helper()
		j, err := s.LookupAuthor(t.Context(), sub, id)
		if err != nil {
			t.Fatal(err)
		}
		return j.Author
	}
	// A request without an author posts as the bound author.
	if r, err := d.SubmitRoute(t.Context(), bound, source, routeBody(t, "bound-1", nil), now); err != nil || r.Result == nil || !r.Result.Durable {
		t.Fatal("bound author", r, err)
	}
	credential, _ := core.NewCredential(gateToken)
	if a := lookup(store.Submission{Author: core.Author{Nick: "alerts", Registered: true}, Credential: credential}, "bound-1"); a != (core.Author{Nick: "alerts", Registered: true}) {
		t.Fatal("bound job author", a)
	}
	// A bound route refuses guest body authors by default but accepts a
	// registered author with its token: AIRC decides that one.
	if _, err := d.SubmitRoute(t.Context(), bound, source, routeBody(t, "guest-1", map[string]string{"nick": "mallory"}), now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("guest on a bound route", err)
	}
	if _, err := d.SubmitRoute(t.Context(), bound, source, routeBody(t, "reg-1", map[string]string{"nick": "carol", "token": gateToken}), now); err != nil {
		t.Fatal("registered body author", err)
	}
	// A guest route posts body authors as guests and refuses anonymous sends.
	if _, err := d.SubmitRoute(t.Context(), guests, source, routeBody(t, "guest-2", map[string]string{"nick": "dave"}), now); err != nil {
		t.Fatal("guest author", err)
	}
	if a := lookup(store.Submission{Author: core.Author{Nick: "dave"}}, "guest-2"); a != (core.Author{Nick: "dave"}) {
		t.Fatal("guest job author", a)
	}
	if _, err := d.SubmitRoute(t.Context(), guests, source, routeBody(t, "anon-1", nil), now); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("anonymous send", err)
	}
	// Route operations still bound requests.
	read := []byte(`{"version":1,"request_id":"read-1","operation":"receipt.get","lookup_id":"guest-2","author":{"nick":"dave"}}`)
	if _, err := d.SubmitRoute(t.Context(), guests, source, read, now); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unconfigured route operation", err)
	}
	if _, err := d.SubmitRoute(t.Context(), guests, source, []byte("{"), now); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("malformed body", err)
	}
}

func TestConnectAppliesListenerPassword(t *testing.T) {
	open, _ := gateFixture(t, config.Listener{Protocol: "http"})
	if open.PasswordRequired() {
		t.Fatal("password without password_file")
	}
	session := NewSession()
	if err := open.Connect(session, core.Password{}); err != nil {
		t.Fatal("open listener refused a connection", err)
	}
	locked, _ := gateFixture(t, config.Listener{Protocol: "http", PasswordFile: "letmein"})
	if !locked.PasswordRequired() {
		t.Fatal("password_file not required")
	}
	wrong, _ := core.NewPassword("nope")
	if err := locked.Connect(NewSession(), wrong); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("wrong password admitted", err)
	}
	if err := locked.Connect(NewSession(), core.Password{}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("missing password admitted", err)
	}
	right, _ := core.NewPassword("letmein")
	admitted := NewSession()
	if err := locked.Connect(admitted, right); err != nil {
		t.Fatal(err)
	}
	// Later requests on the admitted connection need no password.
	if _, err := locked.Submit(t.Context(), Ingress{Source: "192.0.2.1", Session: admitted}, routeBody(t, "after-connect", map[string]string{"nick": "erin"}), time.Unix(1000, 0)); err != nil {
		t.Fatal("admitted connection refused", err)
	}
	if err := (*Dispatcher)(nil).Connect(admitted, right); !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil dispatcher", err)
	}
}
