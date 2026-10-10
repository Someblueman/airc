//go:build darwin || linux

package dispatch

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestSNMPDispatcherProvenanceAndConfiguredOperation(t *testing.T) {
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
	c.Version = 1
	c.Realm = "fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "irc.sock"), Account: "author", AccountTokenFile: secret("account")}}
	target := core.Target{Kind: "room", Name: "#allowed"}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend", BearerTokenFile: secret("bearer"), Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x31}, 32)
	path := filepath.Join(root, "localized")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	engine := []byte{0x80, 0, 0, 1, 2, 3, 4, 5}
	a, err := auth.NewSNMPAuthenticator(v, config.SNMP{Routes: []config.SNMPRoute{{EngineID: hex.EncodeToString(engine), User: "fixture", Authentication: snmpwire.HMACSHA256192, AuthenticationKeyFile: path, ContextEngineID: hex.EncodeToString(engine), Principal: "device", Target: target, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := testsnmp.Trap{Engine: engine, User: "fixture", Boots: 2, Time: 1000, MessageID: 42, RequestID: 1, SHA256: true, Flags: 1, Fields: []testsnmp.Field{{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}}}}
	proof, err := a.Verify(testsnmp.Encode(t, p, key))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for name, d := range map[string]*Dispatcher{"nil": nil, "foreign": {journal: journal, verifier: other, operations: []core.Operation{core.Send}}, "no_journal": {verifier: v, operations: []core.Operation{core.Send}}} {
		t.Run(name, func(t *testing.T) {
			if _, err := d.SNMP(t.Context(), proof, time.Unix(1000, 0)); !errors.Is(err, auth.ErrAuthentication) {
				t.Fatal("unowned proof accepted", err)
			}
		})
	}
	d := &Dispatcher{journal: journal, verifier: v}
	if _, err := d.SNMP(t.Context(), proof, time.Unix(1000, 0)); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unconfigured send accepted", err)
	}
	d.operations = []core.Operation{core.Send}
	result, err := d.SNMP(t.Context(), proof, time.Unix(1000, 0))
	if err != nil || !result.Durable || result.State != core.Queued {
		t.Fatal(result, err)
	}
	again, err := d.SNMP(t.Context(), proof, time.Unix(1001, 0))
	if err != nil || again.RequestID != result.RequestID {
		t.Fatal("replay changed request", err)
	}
	if _, err := journal.Revoke(t.Context(), "device"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SNMP(t.Context(), proof, time.Unix(1001, 0)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("durable revocation bypassed", err)
	}
}
