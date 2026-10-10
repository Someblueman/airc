//go:build darwin || linux

package adapter

import (
	"context"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

func mqttFixtureRoutes() map[string]config.MQTTRoute {
	return map[string]config.MQTTRoute{"airc/send": {Principal: "producer", Topic: "airc/send", Target: core.Target{Kind: "room", Name: "#fixture"}}}
}

func TestMQTTAdmissionPostsPlainRequestAsConnectionPrincipal(t *testing.T) {
	f := newRouteFixture(t)
	packet := mqttwire.Packet{Kind: mqttwire.Publish, ID: 7, Topic: "airc/send", Payload: plainRouteSend}
	requests := make(chan struct{}, 1)
	var upstream string
	for _, id := range []uint16{7, 8, 7} {
		packet.ID, packet.Duplicate = id, id != 7
		if !admitMQTT(t.Context(), packet, f.producer, mqttFixtureRoutes(), f.d, requests) {
			t.Fatal("valid publication refused")
		}
		job, err := f.s.Lookup(t.Context(), f.producer, "plain-route")
		if err != nil || !job.Result.Durable || job.Principal != f.producer.ID() || job.Request.Target == nil || job.Request.Target.Name != "#fixture" || job.Request.Message != "first\nsecond 🙂" || job.Result.SendAttempts != 0 {
			t.Fatal("wrong durable intake", job, err)
		}
		if upstream != "" && upstream != job.UpstreamID {
			t.Fatal("MQTT metadata changed durable identity")
		}
		upstream = job.UpstreamID
	}
	if f.jobs(t, "plain-route") != 1 || len(requests) != 0 {
		t.Fatal("replay duplicated intake or leaked admission slot")
	}
}

func TestMQTTAdmissionRefusesBeforeJournal(t *testing.T) {
	for _, kind := range []string{"topic", "principal", "target", "operation", "malformed", "conflict", "capacity", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f := newRouteFixture(t)
			packet := mqttwire.Packet{Kind: mqttwire.Publish, ID: 1, Topic: "airc/send", Payload: plainRouteSend}
			routes := mqttFixtureRoutes()
			requests := make(chan struct{}, 1)
			ctx := t.Context()
			want := 0
			switch kind {
			case "topic":
				packet.Topic = "unknown"
			case "principal":
				route := routes["airc/send"]
				route.Principal = "other"
				routes["airc/send"] = route
			case "target":
				// The connection principal may send to #other; the topic route refuses it.
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#other"},"message":"hi"}`
			case "operation":
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"receipt.get","lookup_id":"missing"}`
			case "malformed":
				packet.Payload = "not json"
			case "conflict":
				if !admitMQTT(ctx, packet, f.producer, routes, f.d, requests) {
					t.Fatal("original publication refused")
				}
				packet.Payload = `{"version":1,"request_id":"plain-route","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"changed"}`
				want = 1
			case "capacity":
				requests <- struct{}{}
			case "cancelled":
				var cancel func()
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if admitMQTT(ctx, packet, f.producer, routes, f.d, requests) {
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
