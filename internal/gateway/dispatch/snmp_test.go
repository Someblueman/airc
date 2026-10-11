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
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestSNMPDispatcherAdmitsAsBoundAuthor(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "irc.sock")}
	target := core.Target{Kind: "room", Name: "#allowed"}
	reader, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x31}, 32)
	path := filepath.Join(root, "localized")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	engine := []byte{0x80, 0, 0, 1, 2, 3, 4, 5}
	options := config.SNMP{Routes: []config.SNMPRoute{{EngineID: hex.EncodeToString(engine), User: "fixture", Authentication: snmpwire.HMACSHA256192, AuthenticationKeyFile: path, ContextEngineID: hex.EncodeToString(engine), BoundAuthor: config.BoundAuthor{Nick: "snmp-agent"}, Target: target, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert"}}}}}
	a, err := auth.NewSNMPAuthenticator(options)
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
	if _, err := (*Dispatcher)(nil).SNMP(t.Context(), proof, time.Unix(1000, 0)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("nil dispatcher accepted", err)
	}
	if _, err := (&Dispatcher{journal: journal}).SNMP(t.Context(), auth.VerifiedSNMPNotification{}, time.Unix(1000, 0)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("zero proof accepted", err)
	}
	if _, err := (&Dispatcher{journal: journal, backend: reader}).SNMP(t.Context(), proof, time.Unix(1000, 0)); !errors.Is(err, ErrUnsupported) {
		t.Fatal("dispatcher without a route gate accepted", err)
	}
	listener := config.Listener{ID: "snmp", Protocol: "snmp_v3", Operations: []core.Operation{core.Send}, SNMP: &options}
	d, err := ForListener(journal, reader, nil, listener)
	if err != nil || !d.AuthorAccess() {
		t.Fatal("SNMP listener has no route gate", err)
	}
	result, err := d.SNMP(t.Context(), proof, time.Unix(1000, 0))
	if err != nil || !result.Durable || result.State != core.Queued {
		t.Fatal(result, err)
	}
	again, err := d.SNMP(t.Context(), proof, time.Unix(1001, 0))
	if err != nil || again.RequestID != result.RequestID {
		t.Fatal("replay changed request", err)
	}
	job, err := journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "snmp-agent"}}, result.RequestID)
	if err != nil || job.Author != (core.Author{Nick: "snmp-agent"}) || job.Request.Message != "SNMP Alert" {
		t.Fatalf("job not owned by the bound author: %+v %v", job.Author, err)
	}
}
