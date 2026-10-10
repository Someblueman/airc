//go:build darwin || linux

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMQTTRefusesSignedAuthorityFreshnessAndWireBeforeIntake(t *testing.T) {
	for _, kind := range []string{"topic", "target", "principal", "signature", "expired", "future", "operation", "zero_id", "qos0", "retain", "second_connect", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			f := prepareMQTTFixture(t)
			r := core.Request{Version: 1, RequestID: "mqtt-refused", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not be delivered"}
			signer := f.credentials
			if kind == "principal" {
				signer = mailboxOtherSigner(t, f.fileMailboxFixture)
			}
			if kind == "target" {
				r.Target = &core.Target{Kind: "room", Name: "#other"}
				f.config.Principals[0].Targets = append(f.config.Principals[0].Targets, *r.Target)
			}
			if kind == "operation" {
				r = core.Request{Version: 1, RequestID: r.RequestID, Operation: core.ReceiptGet, LookupID: "missing"}
			}
			issued, expires := time.Now().Unix(), time.Now().Unix()+120
			if kind == "expired" {
				issued -= 120
				expires = issued + 60
			}
			if kind == "future" {
				issued += 120
				expires = issued + 120
			}
			payload := signer.validityToken(t, r, issued, expires, false)
			// Prove authority cases carry valid signed intent, so route/publisher
			// refusal is not accidentally a signature or config failure.
			if kind == "principal" || kind == "target" || kind == "operation" {
				v, err := auth.New(f.config)
				if err != nil {
					t.Fatal(err)
				}
				message, err := v.AuthenticateSigned(payload, time.Now())
				if err != nil {
					t.Fatal("authority witness", err)
				}
				if kind == "principal" && message.Identity.ID() != "other-device" {
					t.Fatal("wrong signing witness")
				}
			}
			f.start(t)
			client := f.connect(t)
			packet := mqttPublication(1, "airc/send", payload, false)
			switch kind {
			case "topic":
				packet = mqttPublication(1, "airc/unknown", payload, false)
			case "signature":
				packet = mqttPublication(1, "airc/send", "invalid.signature", false)
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

func TestMQTTConflictingSignedReplayClosesWithoutPUBACK(t *testing.T) {
	f := newMQTTFixture(t)
	client := f.connect(t)
	r := core.Request{Version: 1, RequestID: "mqtt-conflict", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "original MQTT intent"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	payload := f.credentials.validityToken(t, r, issued, expires, false)
	mqttWrite(t, client, mqttPublication(1, "airc/send", payload, false))
	mqttReply(t, client, []byte{0x40, 2, 0, 1})
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	r.Message = "conflicting MQTT intent"
	mqttWrite(t, client, mqttPublication(1, "airc/send", f.credentials.validityToken(t, r, issued, expires, false), true))
	mqttPeerClosed(t, client)
	f.stop(t)
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, r.Target.Name); len(native) != 1 || native[0].ID != event.ID || native[0].Message != "original MQTT intent" {
		t.Fatal("conflict changed native intent", native)
	}
}
