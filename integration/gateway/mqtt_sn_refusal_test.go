//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMQTTSNRefusesRouteAndWireBeforeIntake(t *testing.T) {
	f := prepareMQTTSNFixture(t)
	f.start(t)
	client := f.connect(t)
	for _, kind := range []string{"topic", "target", "malformed", "operation", "nonzero_id", "qos0", "retain", "connect", "oversize", "length"} {
		t.Run(kind, func(t *testing.T) {
			r := core.Request{Version: 1, RequestID: "mqtt-sn-refused-" + kind, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not be delivered"}
			signer := f.credentials
			if kind == "target" {
				r.Target = &core.Target{Kind: "room", Name: "#other"}
			}
			if kind == "operation" {
				r = core.Request{Version: 1, RequestID: r.RequestID, Operation: core.ReceiptGet, LookupID: "missing"}
			}
			payload := signer.token(t, r, true)
			packet := mqttSNPublication(7, payload, false)
			switch kind {
			case "topic":
				packet = mqttSNPublication(8, payload, false)
			case "malformed":
				packet = mqttSNPublication(7, "invalid", false)
			case "nonzero_id":
				packet[7] = 1
			case "qos0":
				packet[4] = 1
			case "retain":
				packet[4] |= 0x10
			case "connect":
				packet = []byte{6, 4, 0, 1, 0, 10}
			case "oversize":
				packet = mqttSNPublication(7, string(bytes.Repeat([]byte{'x'}, 32769)), false)
			case "length":
				packet[2]--
			}
			mqttSNSend(t, client, packet)
			if kind == "topic" {
				if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var reply [16]byte
				n, err := client.Read(reply[:])
				if err != nil || !bytes.Equal(reply[:n], []byte{7, 0x0d, 0, 8, 0, 0, 2}) {
					t.Fatal("topic error", reply[:n], err)
				}
			} else {
				mqttSNNoReply(t, client)
			}
			noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
		})
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal("refusal reached native", native)
	}
	r := core.Request{Version: 1, RequestID: "mqtt-sn-after-refusal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "healthy after MQTT-SN refusals"}
	mqttSNSend(t, client, mqttSNPublication(7, f.credentials.token(t, r, true), false))
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	mqttSNAwaitAccepted(t, f, r.RequestID)
	f.stop(t)
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, r.Target.Name); len(native) != 1 || native[0].ID != event.ID || native[0].Message != r.Message {
		t.Fatal("refusal changed healthy native delivery", native)
	}
}

func TestMQTTSNConflictingReplayPreservesNativeIntent(t *testing.T) {
	f := prepareMQTTSNFixture(t)
	f.start(t)
	client := f.connect(t)
	r := core.Request{Version: 1, RequestID: "mqtt-sn-conflict", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "original MQTT-SN intent"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	mqttSNSend(t, client, mqttSNPublication(7, f.credentials.validityToken(t, r, issued, expires, true), false))
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	r.Message = "conflicting MQTT-SN intent"
	mqttSNAwaitAccepted(t, f, r.RequestID)
	mqttSNSend(t, client, mqttSNPublication(7, f.credentials.validityToken(t, r, issued, expires, true), true))
	mqttSNNoReply(t, client)
	healthy := r
	healthy.RequestID, healthy.Message = "mqtt-sn-after-conflict", "healthy following MQTT-SN conflict"
	mqttSNSend(t, client, mqttSNPublication(7, f.credentials.token(t, healthy, true), false))
	healthyEvent := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == healthy.Message })
	if healthyEvent.From != "fixture-author" || healthyEvent.Target != healthy.Target.Name {
		t.Fatal(healthyEvent)
	}
	mqttSNAwaitAccepted(t, f, healthy.RequestID)
	f.stop(t)
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, r.Target.Name); len(native) != 2 || native[0].ID != event.ID || native[0].Message != "original MQTT-SN intent" || native[1].ID != healthyEvent.ID || native[1].Message != healthy.Message {
		t.Fatal("conflict changed native intent", native)
	}
}
