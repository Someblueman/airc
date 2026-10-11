//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"encoding/asn1"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestSNMPRequestSlotsAndSocketShutdown(t *testing.T) {
	rf := newRouteFixture(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x31}, 32)
	path := filepath.Join(root, "localized")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	engine := []byte{0x80, 0, 0, 1, 2, 3, 4, 5}
	options := config.SNMP{Routes: []config.SNMPRoute{{EngineID: hex.EncodeToString(engine), User: "fixture", Authentication: snmpwire.HMACSHA256192, AuthenticationKeyFile: path, ContextEngineID: hex.EncodeToString(engine), BoundAuthor: config.BoundAuthor{Nick: "sensor"}, Target: core.Target{Kind: "room", Name: "#fixture"}, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert"}}}}}
	a, err := auth.NewSNMPAuthenticator(options)
	if err != nil {
		t.Fatal(err)
	}
	f := struct{ d *dispatch.Dispatcher }{rf.listener(t, config.Listener{ID: "snmp", Protocol: "snmp_v3", Operations: []core.Operation{core.Send}, SNMP: &options})}
	p := testsnmp.Trap{Engine: engine, User: "fixture", Boots: 2, Time: 1000, MessageID: 42, RequestID: 1, SHA256: true, Flags: 1, Fields: []testsnmp.Field{{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}}}}
	raw := testsnmp.Encode(t, p, key)
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	admitSNMP(t.Context(), raw, a, f.d, slots)
	if len(slots) != 1 {
		t.Fatal("busy slot consumed")
	}
	// A valid busy packet must not seed the engine clock. Otherwise this
	// deliberately older first packet would be rejected rather than admitted.
	<-slots
	p.Time = 0
	admitSNMP(t.Context(), testsnmp.Encode(t, p, key), a, f.d, slots)
	proof, err := a.Verify(testsnmp.Encode(t, p, key))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.SNMP(t.Context(), proof, time.Now()); err != nil {
		t.Fatal("busy packet changed durable clock", err)
	}
	if len(slots) != 0 {
		t.Fatal("slot leaked")
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeSNMP(ctx, conn, a, f.d, slots) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("socket handler did not join")
	}
	if _, _, err := conn.ReadFrom(make([]byte, 1)); err == nil {
		t.Fatal("owned socket left open")
	}
}
