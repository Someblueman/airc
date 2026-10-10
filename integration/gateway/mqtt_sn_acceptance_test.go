//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMQTTSNPreIntakeLossNativeReplayAndShutdown(t *testing.T) {
	f := prepareMQTTSNFixture(t)
	f.start(t)
	r := core.Request{Version: 1, RequestID: "mqtt-sn-loss", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "MQTT-SN exact native body\n世界 🙂"}
	payload := f.credentials.token(t, r, true)
	// Real independent UDP relay deliberately discards the first received packet
	// before ingress. A successful producer write therefore does not imply intake.
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
	first := mqttSNPublication(7, payload, false)
	mqttSNSend(t, producer, first)
	if err := relay.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 65536)
	n, _, err := relay.ReadFromUDP(buffer)
	if err != nil || !bytes.Equal(buffer[:n], first) {
		t.Fatal("loss relay did not receive exact publication", err)
	}
	noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 0 {
		t.Fatal("discarded packet delivered", native)
	}
	// Retry retains signed intent, but changes DUP and travels through the relay.
	retry := mqttSNPublication(7, payload, true)
	mqttSNSend(t, producer, retry)
	n, _, err = relay.ReadFromUDP(buffer)
	if err != nil || !bytes.Equal(buffer[:n], retry) {
		t.Fatal("relay retry", err)
	}
	destination, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := relay.WriteToUDP(buffer[:n], destination); err != nil || n != len(retry) {
		t.Fatal("relay forwarding", n, err)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	mqttSNAwaitAccepted(t, f, r.RequestID)
	f.stop(t)
	// Restart and a new UDP source must replay the same durable application ID.
	f.start(t)
	client := f.connect(t)
	mqttSNSend(t, client, retry)
	mqttSNNoReply(t, client)
	second := r
	second.RequestID, second.Message = "mqtt-sn-health", "second MQTT-SN native intent Ω"
	mqttSNSend(t, client, mqttSNPublication(7, f.credentials.token(t, second, true), false))
	secondEvent := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == second.Message })
	if secondEvent.From != "fixture-author" || secondEvent.Target != second.Target.Name {
		t.Fatal(secondEvent)
	}
	mqttSNAwaitAccepted(t, f, second.RequestID)
	f.stop(t)
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("journal was not released", err)
	}
	defer s.Close()
	for _, expected := range []struct {
		request core.Request
		id      string
	}{{r, event.ID}, {second, secondEvent.ID}} {
		job, err := s.Lookup(t.Context(), fixtureIdentity(t, f.config), expected.request.RequestID)
		if err != nil || !job.Result.Durable || job.Result.MessageID != expected.id || job.Result.SendAttempts != 1 || job.Request.Message != expected.request.Message {
			t.Fatal("durable native replay", job, err)
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 2 || native[0].ID != event.ID || native[0].Message != r.Message || native[1].ID != secondEvent.ID || native[1].Message != second.Message {
		t.Fatal("duplicated native delivery", native)
	}
}
