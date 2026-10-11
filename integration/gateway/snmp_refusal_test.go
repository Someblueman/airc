//go:build darwin || linux

package gateway_test

import (
	"encoding/asn1"
	"testing"

	"github.com/Someblueman/airc/internal/testsnmp"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSNMPNativeRefusalsAndTimeliness(t *testing.T) {
	f := prepareSNMPFixture(t)
	f.start(t)
	client := f.connect(t)
	initial := f.trap("seed", 1, 2, 1000)
	mqttSNSend(t, client, testsnmp.Encode(t, initial, f.key))
	next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "SNMP Alert\nMessage: seed" })
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, snmpRequestID(t, initial))
	cases := []struct {
		name   string
		mutate func(*testsnmp.Trap)
	}{
		{"user", func(p *testsnmp.Trap) { p.User = "other" }},
		{"engine", func(p *testsnmp.Trap) { p.Engine = []byte{0x80, 0, 0, 1, 2, 3, 4, 6} }},
		{"context", func(p *testsnmp.Trap) { p.Context = "other" }},
		{"old_time", func(p *testsnmp.Trap) { p.Time = 849 }},
		{"old_boot", func(p *testsnmp.Trap) { p.Boots = 1 }},
		{"exhausted_boot", func(p *testsnmp.Trap) { p.Boots = 2147483647 }},
		{"notification", func(p *testsnmp.Trap) { p.Fields[1].Data = []byte{43, 6, 1, 4, 1, 123, 9} }},
		{"unknown_field", func(p *testsnmp.Trap) { p.Fields[2].OID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 9} }},
		{"missing_field", func(p *testsnmp.Trap) { p.Fields = p.Fields[:2] }},
		{"binary_text", func(p *testsnmp.Trap) { p.Fields[2].Data = []byte{255} }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := f.trap("refused "+tc.name, 10+i, 2, 1000)
			tc.mutate(&p)
			mqttSNSend(t, client, testsnmp.Encode(t, p, f.key))
			mqttSNNoReply(t, client)
		})
	}
	raw := testsnmp.Encode(t, f.trap("bad MAC", 30, 2, 1000), f.key)
	raw[len(raw)-1] ^= 1
	mqttSNSend(t, client, raw)
	mqttSNSend(t, client, make([]byte, 8193))
	health := f.trap("fence", 40, 2, 1001)
	mqttSNSend(t, client, testsnmp.Encode(t, health, f.key))
	next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "SNMP Alert\nMessage: fence" })
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, snmpRequestID(t, health))
	f.stop(t)
	if history := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(history) != 2 {
		t.Fatal("refused trap delivered", history)
	}
}
