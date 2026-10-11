//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testsnmp"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSNMPNativeLossReplayRestartAndShutdown(t *testing.T) {
	f := prepareSNMPFixture(t)
	f.start(t)
	trap := f.trap("SNMP exact native 世界\nbody 🙂", 1, 2, 1000)
	raw := testsnmp.Encode(t, trap, f.key)
	id := snmpRequestID(t, trap)
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	producer, err := net.DialUDP("udp", nil, relay.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	mqttSNSend(t, producer, raw)
	if err := relay.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 8193)
	n, _, err := relay.ReadFromUDP(buffer)
	if err != nil || !bytes.Equal(buffer[:n], raw) {
		t.Fatal("loss relay", err)
	}
	noMailboxJob(t, f.fileMailboxFixture, id)
	if history := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(history) != 0 {
		t.Fatal("discarded trap delivered", history)
	}
	// Retry the exact authenticated packet through a real independent UDP relay.
	mqttSNSend(t, producer, raw)
	n, _, err = relay.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	address, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	if wrote, err := relay.WriteToUDP(buffer[:n], address); err != nil || wrote != n {
		t.Fatal(wrote, err)
	}
	body := "SNMP Alert\nMessage: " + string(trap.Fields[2].Data)
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
	if event.From != "fixture-author" || event.Target != "#gateway-fixture" {
		t.Fatal(event)
	}
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, id)
	client := f.connect(t)
	mqttSNSend(t, client, raw)
	mqttSNNoReply(t, client)
	f.stop(t)
	f.start(t)
	client = f.connect(t)
	mqttSNSend(t, client, raw)
	mqttSNNoReply(t, client)
	health := f.trap("health Ω", 2, 2, 1001)
	mqttSNSend(t, client, testsnmp.Encode(t, health, f.key))
	second := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "SNMP Alert\nMessage: health Ω" })
	if second.From != "fixture-author" || second.Target != "#gateway-fixture" {
		t.Fatal(second)
	}
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, snmpRequestID(t, health))
	f.stop(t)
	journal, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("journal not released", err)
	}
	defer journal.Close()
	for _, want := range []struct{ id, body, messageID string }{{id, body, event.ID}, {snmpRequestID(t, health), second.Message, second.ID}} {
		job, err := journal.LookupAuthor(t.Context(), fixtureAuthorSubmission(t), want.id)
		if err != nil || job.Request.Message != want.body || job.Result.MessageID != want.messageID || job.Result.SendAttempts != 1 {
			t.Fatal("replay changed native work", job, err)
		}
	}
	if history := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(history) != 2 || history[0].ID != event.ID || history[1].ID != second.ID {
		t.Fatal("native duplicates", history)
	}
}
