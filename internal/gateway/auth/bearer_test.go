//go:build unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func fixtureConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	root := privateTemp(t)
	secret := func(name string) string {
		path := filepath.Join(root, name)
		if err := CreateCredential(path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.Bindings = []config.Binding{{ID: "fixture-backend", Network: "unix", Address: filepath.Join(root, "irc.sock"), Account: "fixture-author", AccountTokenFile: secret("account")}}
	c.Principals = []config.Principal{{ID: "device", Binding: "fixture-backend", BearerTokenFile: secret("bearer"), Operations: []core.Operation{core.Send, core.Reply, core.HistoryRead, core.ReceiptGet}, Targets: []core.Target{{Kind: "room", Name: "#Allowed"}, {Kind: "direct", Name: "Alice"}}}}
	token, err := ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	return c, token
}

func TestBearerResolutionAndExactPermissions(t *testing.T) {
	t.Parallel()
	c, token := fixtureConfig(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID() != "device" || identity.Binding() != "fixture-backend" {
		t.Fatal("wrong principal or account binding")
	}
	for _, bad := range []string{"", strings.Repeat("x", 64), strings.Repeat("0", 64), token + "\n"} {
		if _, err := v.Authenticate(bad); !errors.Is(err, ErrAuthentication) || strings.Contains(err.Error(), token) {
			t.Fatal("invalid credential accepted or reflected")
		}
	}
	requests := []core.Request{
		{Version: 1, RequestID: "r", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Message: "hello"},
		{Version: 1, RequestID: "r", Operation: core.Reply, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Message: "hello", ParentID: strings.Repeat("a", 32)},
		{Version: 1, RequestID: "r", Operation: core.HistoryRead, Target: &core.Target{Kind: "direct", Name: "ALICE"}, Limit: 5},
		{Version: 1, RequestID: "r", Operation: core.ReceiptGet, LookupID: "original"},
	}
	for _, r := range requests {
		if err := identity.Authorize(r); err != nil {
			t.Fatalf("allowed %s: %v", r.Operation, err)
		}
	}
	requests[0].Target.Name = "#allowed"
	if !errors.Is(identity.Authorize(requests[0]), ErrForbidden) {
		t.Fatal("room case boundary bypassed")
	}
	requests[0].Target.Name = "#Denied"
	if !errors.Is(identity.Authorize(requests[0]), ErrForbidden) {
		t.Fatal("target permission bypassed")
	}
	requests[0].Operation = core.Watch
	requests[0].Message = ""
	requests[0].Limit = 5
	requests[0].Target.Name = "#Allowed"
	if !errors.Is(identity.Authorize(requests[0]), ErrForbidden) {
		t.Fatal("operation permission bypassed")
	}
	if !errors.Is((Identity{}).Authorize(requests[1]), ErrForbidden) {
		t.Fatal("unverified identity accepted")
	}
	// Neither caller-owned config nor returned capabilities can modify a live
	// verifier's already established permissions.
	c.Principals[0].Targets[0].Name = "#Changed"
	c.Principals[0].Operations[0] = core.Watch
	ops := identity.Operations()
	ops[0] = core.Watch
	if err := identity.Authorize(requests[1]); err != nil {
		t.Fatal("permission snapshot mutated")
	}
}

func TestRotationRevocationAndCredentialAmbiguity(t *testing.T) {
	t.Parallel()
	c, oldToken := fixtureConfig(t)
	old, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	before, err := old.Authenticate(oldToken)
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(filepath.Dir(c.Principals[0].BearerTokenFile), "rotated")
	if err := CreateCredential(newPath); err != nil {
		t.Fatal(err)
	}
	c.Principals[0].BearerTokenFile = newPath
	rotated, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	newToken, _ := ReadToken(newPath)
	after, err := rotated.Authenticate(newToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Authenticate(oldToken); !errors.Is(err, ErrAuthentication) {
		t.Fatal("old credential survived rotation")
	}
	if before.ID() != after.ID() || before.Binding() != after.Binding() {
		t.Fatal("rotation changed principal or binding")
	}
	scope := core.Author{Nick: c.Bindings[0].Account, Registered: true}.Scope()
	a, _ := core.UpstreamID(c.Realm, scope, "stable")
	b, _ := core.UpstreamID(c.Realm, scope, "stable")
	if a == "" || a != b {
		t.Fatal("rotation changed author or stable operation ID")
	}
	c.Principals[0].Revoked = true
	revoked, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revoked.Authenticate(newToken); !errors.Is(err, ErrAuthentication) {
		t.Fatal("revoked credential accepted")
	}
	// Two different files containing the same key must not resolve ambiguously.
	duplicate := filepath.Join(filepath.Dir(newPath), "duplicate")
	if err := os.WriteFile(duplicate, []byte(strings.ToUpper(newToken)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := c.Principals[0]
	p.ID, p.BearerTokenFile, p.Revoked = "other", duplicate, false
	c.Principals = append(c.Principals, p)
	if _, err := New(c); err == nil {
		t.Fatal("ambiguous credential accepted")
	}
}
