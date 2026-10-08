//go:build darwin || linux

package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestMatrixDispatcherOwnedProofReplayFreshnessAndRevocation(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	secret := func(name string) string {
		path := filepath.Join(root, name)
		if err := auth.CreateCredential(path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: secret("account")}}
	target := core.Target{Kind: "room", Name: "#Allowed"}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend", BearerTokenFile: secret("bearer"), Operations: []core.Operation{core.Send, core.ReceiptGet}, Targets: []core.Target{target}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	proof := matrixDispatchProof(t, v, c, "device", "$event-1", "whole 世界 body  ", now)
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for name, d := range map[string]*Dispatcher{
		"nil":         nil,
		"foreign":     {journal: journal, verifier: foreign, operations: []core.Operation{core.Send}},
		"no_journal":  {verifier: v, operations: []core.Operation{core.Send}},
		"no_verifier": {journal: journal, operations: []core.Operation{core.Send}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := d.Matrix(t.Context(), proof, now); !errors.Is(err, auth.ErrAuthentication) {
				t.Fatal("unowned proof accepted", err)
			}
		})
	}
	d, err := New(journal, v, nil, nil, []core.Operation{core.Send})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Matrix(t.Context(), auth.VerifiedMatrixMessage{}, now); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("zero proof", err)
	}
	unsupported, err := New(journal, v, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsupported.Matrix(t.Context(), proof, now); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unconfigured send", err)
	}
	result, err := d.Matrix(t.Context(), proof, now)
	if err != nil || !result.Durable || result.State != core.Queued {
		t.Fatal(result, err)
	}
	again, err := d.Matrix(t.Context(), proof, now.Add(time.Second))
	if err != nil || again.RequestID != result.RequestID {
		t.Fatal("replay", again, err)
	}
	job, err := journal.Lookup(t.Context(), proof.Identity(), proof.RequestID())
	if err != nil || job.Request.Message != proof.Body() || *job.Request.Target != target {
		t.Fatal("whole intent", job, err)
	}
	if _, err := d.Matrix(t.Context(), proof, proof.ReplayDeadline()); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("expiry renewed", err)
	}
	if _, err := journal.Revoke(t.Context(), "device"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Matrix(t.Context(), proof, now.Add(time.Second)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revocation bypassed", err)
	}
}
