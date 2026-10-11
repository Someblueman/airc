//go:build darwin || linux

package gateway_test

import (
	"encoding/asn1"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testsnmp"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSNMPNativeExactBodyBound(t *testing.T) {
	f := prepareSNMPFixture(t)
	notification := &f.config.Listeners[0].SNMP.Routes[0].Notifications[0]
	notification.Fields = nil
	p := f.trap("", 1, 2, 1000)
	p.Fields = p.Fields[:2]
	body := "SNMP Alert"
	remaining := 4096 - len(body)
	for i := 0; i < 8; i++ {
		oid := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 10 + i}
		name := fmt.Sprintf("F%d", i)
		size := 512
		if i == 7 {
			size = remaining - len("\n"+name+": ")
		}
		remaining -= len("\n"+name+": ") + size
		value := strings.Repeat("x", size)
		notification.Fields = append(notification.Fields, config.SNMPField{OID: oid.String(), Name: name, Type: "text", Required: true})
		p.Fields = append(p.Fields, testsnmp.Field{OID: oid, Tag: 4, Data: []byte(value)})
		body += "\n" + name + ": " + value
	}
	if len(body) != 4096 || remaining != 0 {
		t.Fatal("fixture size")
	}
	f.start(t)
	client := f.connect(t)
	mqttSNSend(t, client, testsnmp.Encode(t, p, f.key))
	first := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
	if first.From != "fixture-author" || first.Target != "#gateway-fixture" {
		t.Fatal(first)
	}
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, snmpRequestID(t, p))
	p.RequestID = 2
	p.Fields[len(p.Fields)-1].Data = append(p.Fields[len(p.Fields)-1].Data, 'x')
	mqttSNSend(t, client, testsnmp.Encode(t, p, f.key))
	mqttSNNoReply(t, client)
	p.RequestID = 3
	fence := "SNMP Alert"
	for i := 2; i < len(p.Fields); i++ {
		p.Fields[i].Data = []byte("fence")
		fence += fmt.Sprintf("\nF%d: fence", i-2)
	}
	mqttSNSend(t, client, testsnmp.Encode(t, p, f.key))
	next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == fence })
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, snmpRequestID(t, p))
	f.stop(t)
	if history := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(history) != 2 || history[0].Message != body {
		t.Fatal("bound or truncation failed", history)
	}
}
