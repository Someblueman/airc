//go:build darwin || linux

package gateway_test

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMQTTRefusesRouteAuthorityAndWireBeforeIntake(t *testing.T) {
	for _, kind := range []string{"topic", "target", "principal", "malformed", "operation", "zero_id", "qos0", "retain", "second_connect", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			f := prepareMQTTFixture(t)
			r := core.Request{Version: 1, RequestID: "mqtt-refused", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not be delivered"}
			switch kind {
			case "target":
				// The connection principal may send to #other; only the topic route refuses it.
				r.Target = &core.Target{Kind: "room", Name: "#other"}
				f.config.Principals[0].Targets = append(f.config.Principals[0].Targets, *r.Target)
			case "operation":
				r = core.Request{Version: 1, RequestID: r.RequestID, Operation: core.ReceiptGet, LookupID: "missing"}
			}
			publisher, bearer := f.config.Principals[0].ID, f.bearer
			if kind == "principal" {
				// Another authenticated publisher with its own grant cannot use this topic.
				other := f.config.Principals[0]
				other.ID, other.BearerTokenFile = "other-device", filepath.Join(filepath.Dir(other.BearerTokenFile), "other-bearer")
				if err := auth.CreateCredential(other.BearerTokenFile); err != nil {
					t.Fatal(err)
				}
				var err error
				if bearer, err = auth.ReadToken(other.BearerTokenFile); err != nil {
					t.Fatal(err)
				}
				f.config.Principals = append(f.config.Principals, other)
				routes := &f.config.Listeners[0].MQTT.Routes
				*routes = append(*routes, config.MQTTRoute{Principal: other.ID, Topic: "airc/other", Target: *r.Target})
				publisher = other.ID
			}
			payload := mqttRequest(t, r)
			f.start(t)
			client := f.connectAs(t, publisher, bearer)
			packet := mqttPublication(1, "airc/send", payload, false)
			switch kind {
			case "topic":
				packet = mqttPublication(1, "airc/unknown", payload, false)
			case "malformed":
				packet = mqttPublication(1, "airc/send", `{"version":1,"request_id":"mqtt-refused",`, false)
			case "zero_id":
				packet = mqttPublication(0, "airc/send", payload, false)
			case "qos0":
				packet[0] = 0x30
			case "retain":
				packet[0] |= 1
			case "second_connect":
				packet = []byte{0x10, 0}
			case "oversize":
				packet = []byte{0x32, 0x85, 0x81, 0x02} // 32,901 declared bytes; no body.
			}
			mqttWrite(t, client, packet)
			mqttPeerClosed(t, client)
			noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
			// A fresh authenticated connection remains useful after refusal.
			healthy := f.connect(t)
			mqttWrite(t, healthy, []byte{0xc0, 0})
			mqttReply(t, healthy, []byte{0xd0, 0})
			f.stop(t)
			if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(native) != 0 {
				t.Fatal("refusal reached native", native)
			}
		})
	}
}

func TestMQTTConflictingReplayClosesWithoutPUBACK(t *testing.T) {
	f := newMQTTFixture(t)
	client := f.connect(t)
	r := core.Request{Version: 1, RequestID: "mqtt-conflict", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "original MQTT intent"}
	mqttWrite(t, client, mqttPublication(1, "airc/send", mqttRequest(t, r), false))
	mqttReply(t, client, []byte{0x40, 2, 0, 1})
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	r.Message = "conflicting MQTT intent"
	mqttWrite(t, client, mqttPublication(1, "airc/send", mqttRequest(t, r), true))
	mqttPeerClosed(t, client)
	f.stop(t)
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 1 || native[0].ID != event.ID || native[0].Message != "original MQTT intent" {
		t.Fatal("conflict changed native intent", native)
	}
}
