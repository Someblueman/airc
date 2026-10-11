//go:build darwin || linux

package gateway_test

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testsnmp"
)

const snmpEngine = "8000000102030405"
const snmpPassword = "test-only-fixture-password"

type snmpFixture struct {
	*mqttSNFixture
	engine, key []byte
}

func prepareSNMPFixture(t *testing.T) *snmpFixture {
	t.Helper()
	f := &snmpFixture{mqttSNFixture: prepareMQTTSNFixture(t)}
	var err error
	f.engine, err = hex.DecodeString(snmpEngine)
	if err != nil {
		t.Fatal(err)
	}
	f.key = testsnmp.LocalizedKey(snmpPassword, f.engine, false)
	path := filepath.Join(filepath.Dir(f.config.StateDir), "snmp-localized-key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(f.key)), 0600); err != nil {
		t.Fatal(err)
	}
	l := &f.config.Listeners[0]
	l.Protocol = "snmp_v3"
	l.MQTTSN = nil
	l.SNMP = &config.SNMP{Routes: []config.SNMPRoute{{EngineID: snmpEngine, User: "fixture", Authentication: snmpwire.HMACSHA196, AuthenticationKeyFile: path, ContextEngineID: snmpEngine, BoundAuthor: config.BoundAuthor{Nick: fixtureAuthorNick, AccountTokenFile: fixtureAuthorTokenFile(t, f.config, "snmp-author")}, Target: core.Target{Kind: "room", Name: "#gateway-fixture"}, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert", Fields: []config.SNMPField{{OID: "1.3.6.1.4.1.123.2", Name: "Message", Type: "text", Required: true}}}}}}}
	return f
}
func (f *snmpFixture) trap(body string, id, boots, clock int) testsnmp.Trap {
	return testsnmp.Trap{Engine: f.engine, User: "fixture", Boots: boots, Time: clock, MessageID: 42, RequestID: id, Flags: 1, Fields: []testsnmp.Field{{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 2}, Tag: 4, Data: []byte(body)}}}
}
func snmpRequestID(t *testing.T, p testsnmp.Trap) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Domain, Engine, User string
		Boots, Time          uint32
		RequestID            int32
	}{"airc-snmp-v3", hex.EncodeToString(p.Engine), p.User, uint32(p.Boots), uint32(p.Time), int32(p.RequestID)})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
