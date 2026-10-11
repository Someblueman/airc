//go:build darwin || linux

package dispatch

import (
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

// bridgeDispatchFixture is a gateway with one author upstream
// and a bridge dispatcher routing to #Allowed.
func bridgeDispatchFixture(t *testing.T, ops ...core.Operation) (config.Config, *Dispatcher, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "airc.sock")}
	token := filepath.Join(root, "carol.token")
	if err := auth.CreateCredential(token); err != nil {
		t.Fatal(err)
	}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	d, err := ForBridge(journal, b, nil, "network", []config.BridgeRoute{{Room: "#external", Target: core.Target{Kind: "room", Name: "#Allowed"}, Operations: ops}})
	if err != nil {
		t.Fatal(err)
	}
	return c, d, journal, token
}

func TestIRCBridgeSendIsAGuestAuthorJob(t *testing.T) {
	c, d, journal, token := bridgeDispatchFixture(t, core.Send)
	now := time.Now().UTC().Truncate(time.Millisecond)
	proof := ircDispatchProof(t, c, "alice", "event-1", "whole 世界 body  ", now)
	for name, d := range map[string]*Dispatcher{"nil": nil, "no_gate": {journal: journal}} {
		t.Run(name, func(t *testing.T) {
			if _, err := d.Bridge(t.Context(), proof.Event(), now); !errors.Is(err, auth.ErrAuthentication) {
				t.Fatal("bridge without a gate", err)
			}
		})
	}
	if _, err := d.Bridge(t.Context(), auth.BridgeEvent{}, now); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("zero event", err)
	}
	result, err := d.Bridge(t.Context(), proof.Event(), now)
	if err != nil || result.Result == nil || !result.Result.Durable || result.Result.State != core.Queued {
		t.Fatal(result, err)
	}
	// A replay of the same IRC message (reconnect, duplicate delivery) is the
	// same job, whatever nickname the sender has now.
	again, err := d.Bridge(t.Context(), proof.Event(), now.Add(time.Second))
	if err != nil || again.Result.RequestID != result.Result.RequestID {
		t.Fatal("replay", again, err)
	}
	guest := store.Submission{Author: core.Author{Nick: "irc-alice"}}
	job, err := journal.LookupAuthor(t.Context(), guest, proof.RequestID())
	if err != nil || job.Request.Message != proof.Body() || *job.Request.Target != (core.Target{Kind: "room", Name: "#Allowed"}) || job.Author != guest.Author || job.Listener != "network" {
		t.Fatal("guest job", job, err)
	}
	// A mapped account posts as the registered account with its token.
	mapped := ircDispatchProof(t, c, "carol", "event-2", "as carol", now, config.BridgeAccount{External: "carol", Nick: "carol", AccountTokenFile: token})
	if _, err := d.Bridge(t.Context(), mapped.Event(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "carol", Registered: true}, Credential: mapped.Event().Author().Credential}, mapped.RequestID()); err != nil {
		t.Fatal("mapped account job", err)
	}
	if _, err := journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "irc-carol"}}, mapped.RequestID()); err == nil {
		t.Fatal("mapped account also posted as a guest")
	}
}

func TestBridgeReadsRunOnlyWhereTheRouteAllows(t *testing.T) {
	c, d, _, _ := bridgeDispatchFixture(t, core.Send)
	now := time.Now().UTC().Truncate(time.Millisecond)
	read := ircDispatchProof(t, c, "alice", "event-3", `AIRC-READ/1 {"operation":"history.read"}`, now)
	if !read.Event().IsRead() {
		t.Fatal("read marker")
	}
	if _, err := d.Bridge(t.Context(), read.Event(), now); !errors.Is(err, ErrUnsupported) {
		t.Fatal("read outside the route", err)
	}
	malformed := ircDispatchProof(t, c, "alice", "event-4", `AIRC-READ/1 {"operation":`, now)
	if _, err := d.Bridge(t.Context(), malformed.Event(), now); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("malformed read", err)
	}
}

func TestMatrixBridgeSenderIsTheAuthor(t *testing.T) {
	c, d, journal, _ := bridgeDispatchFixture(t, core.Send)
	now := time.Now().UTC().Truncate(time.Millisecond)
	proof := matrixDispatchProof(t, c, "@alice:localhost", "$event", "from matrix", now)
	result, err := d.Bridge(t.Context(), proof.Event(), now)
	if err != nil || !result.Result.Durable {
		t.Fatal(result, err)
	}
	job, err := journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "mx-alice"}}, proof.RequestID())
	if err != nil || job.Request.Message != "from matrix" {
		t.Fatal("matrix guest job", job, err)
	}
	foreign := matrixDispatchProof(t, c, "@alice:elsewhere.org", "$other", "from elsewhere", now)
	if a := foreign.Event().Author().Author; a.Nick == "mx-alice" || a.Registered {
		t.Fatal("foreign homeserver shares the local guest", a)
	}
}
