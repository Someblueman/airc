//go:build darwin || linux

package adapter

import (
	"context"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

func mqttFixtureRoutes() map[string]config.MQTTRoute {
	return map[string]config.MQTTRoute{"airc/send": {Topic: "airc/send"}}
}

func mqttFixtureListener() config.Listener {
	routes := []config.MQTTRoute{mqttFixtureRoutes()["airc/send"]}
	return config.Listener{ID: "mqtt", Protocol: "mqtt", Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}, MQTT: &config.MQTT{Routes: routes}}
}

func TestMQTTAdmissionPostsPlainRequestAsItsAuthor(t *testing.T) {
	f := newRouteFixture(t)
	d := f.listener(t, mqttFixtureListener())
	in := dispatch.Ingress{Source: "192.0.2.1", Session: dispatch.NewSession()}
	packet := mqttwire.Packet{Kind: mqttwire.Publish, QoS: 1, ID: 7, Topic: "airc/send", Payload: plainRouteSend}
	requests := make(chan struct{}, 1)
	// QoS 1 publications, a duplicate and a QoS 0 resend of the same request
	// are one durable job.
	for _, p := range []struct {
		qos byte
		id  uint16
	}{{1, 7}, {1, 8}, {0, 0}} {
		packet.QoS, packet.ID, packet.Duplicate = p.qos, p.id, p.id == 8
		if !admitMQTT(t.Context(), packet, in, mqttFixtureRoutes(), d, requests) {
			t.Fatal("valid publication refused")
		}
	}
	if f.jobs(t, "plain-route") != 1 || len(requests) != 0 {
		t.Fatal("replay duplicated intake or leaked admission slot")
	}
	if a := f.owner(t, "route-guest", false, "plain-route"); a != (core.Author{Nick: "route-guest"}) {
		t.Fatal("wrong author", a)
	}
	registered := `{"version":1,"request_id":"registered","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hi","author":{"nick":"carol","token":"` + authorTestToken + `"}}`
	packet.Payload = registered
	if !admitMQTT(t.Context(), packet, in, mqttFixtureRoutes(), d, requests) {
		t.Fatal("registered author refused")
	}
	if a := f.owner(t, "carol", true, "registered"); a != (core.Author{Nick: "carol", Registered: true}) {
		t.Fatal("wrong registered author", a)
	}
}

func TestMQTTAdmissionRefusesBeforeJournal(t *testing.T) {
	for _, kind := range []string{"topic", "target", "operation", "anonymous", "malformed", "conflict", "capacity", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f := newRouteFixture(t)
			d := f.listener(t, mqttFixtureListener())
			in := dispatch.Ingress{Source: "192.0.2.1", Session: dispatch.NewSession()}
			packet := mqttwire.Packet{Kind: mqttwire.Publish, QoS: 1, ID: 1, Topic: "airc/send", Payload: plainRouteSend}
			routes := mqttFixtureRoutes()
			requests := make(chan struct{}, 1)
			ctx := t.Context()
			want := 0
			switch kind {
			case "topic":
				packet.Topic = "unknown"
			case "target":
				// The topic's route names #fixture.
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#other"},"message":"hi","author":{"nick":"route-guest"}}`
			case "operation":
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"receipt.get","lookup_id":"missing","author":{"nick":"route-guest"}}`
			case "anonymous":
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hi"}`
			case "malformed":
				packet.Payload = "not json"
			case "conflict":
				if !admitMQTT(ctx, packet, in, routes, d, requests) {
					t.Fatal("original publication refused")
				}
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"changed","author":{"nick":"route-guest"}}`
				want = 1
			case "capacity":
				requests <- struct{}{}
			case "cancelled":
				var cancel func()
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if admitMQTT(ctx, packet, in, routes, d, requests) {
				t.Fatal("unsafe publication admitted")
			}
			if f.jobs(t, "plain-route") != want {
				t.Fatal("refusal touched journal")
			}
			slots := 0
			if kind == "capacity" {
				slots = 1
			}
			if len(requests) != slots {
				t.Fatal("refusal changed someone else's capacity")
			}
		})
	}
}
