//go:build darwin || linux

package store

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testsnmp"
)

// snmpFactory makes notifications from an engine/user grant posting as the
// guest nick.
func snmpFactory(t *testing.T, c config.Config, user, nick string, keyByte ...byte) func(int, int, int, string, ...int) auth.VerifiedSNMPNotification {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "key")
	value := byte(0x31)
	if len(keyByte) > 0 {
		value = keyByte[0]
	}
	key := bytes.Repeat([]byte{value}, 32)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	engine := []byte{0x80, 0, 0, 1, 2, 3, 4, 5}
	a, err := auth.NewSNMPAuthenticator(config.SNMP{Routes: []config.SNMPRoute{{EngineID: hex.EncodeToString(engine), User: user, Authentication: snmpwire.HMACSHA256192, AuthenticationKeyFile: path, ContextEngineID: hex.EncodeToString(engine), BoundAuthor: config.BoundAuthor{Nick: nick}, Target: core.Target{Kind: "room", Name: "#Allowed"}, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert", Fields: []config.SNMPField{{OID: "1.3.6.1.4.1.123.2", Name: "Message", Type: "text", Required: true}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return func(boots, clock, id int, body string, messageID ...int) auth.VerifiedSNMPNotification {
		envelopeID := 42
		if len(messageID) > 0 {
			envelopeID = messageID[0]
		}
		p := testsnmp.Trap{Engine: engine, User: user, Boots: boots, Time: clock, MessageID: envelopeID, RequestID: id, SHA256: true, Flags: 1, Fields: []testsnmp.Field{{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 2}, Tag: 4, Data: []byte(body)}}}
		proof, err := a.Verify(testsnmp.Encode(t, p, key))
		if err != nil {
			t.Fatal(err)
		}
		return proof
	}
}
func assertSNMPCount(t *testing.T, s *Store, table string, want int) {
	t.Helper()
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
		t.Fatalf("%s count %d want %d: %v", table, count, want, err)
	}
}
