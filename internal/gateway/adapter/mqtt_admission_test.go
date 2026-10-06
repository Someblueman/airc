//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestMQTTAdmissionPreservesDurableReplay(t *testing.T) {
	d, s, identity, body, _ := oscoreRPCFixture(t)
	var frame FrameRequest
	if err := json.Unmarshal(body, &frame); err != nil {
		t.Fatal(err)
	}
	packet := mqttwire.Packet{Kind: mqttwire.Publish, ID: 7, Topic: "airc/send", Payload: frame.Envelope}
	routes := map[string]config.MQTTRoute{packet.Topic: {Principal: identity.ID(), Target: core.Target{Kind: "room", Name: "#fixture"}}}
	requests := make(chan struct{}, 1)
	var upstream string
	for _, id := range []uint16{7, 8, 7} {
		packet.ID, packet.Duplicate = id, id != 7
		if !admitMQTT(t.Context(), packet, identity, routes, d, requests) {
			t.Fatal("valid publication refused")
		}
		job, err := s.Lookup(t.Context(), identity, "oscore-rpc-message")
		if err != nil || !job.Result.Durable || job.Principal != identity.ID() || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 {
			t.Fatal("wrong durable intake", job, err)
		}
		if upstream != "" && upstream != job.UpstreamID {
			t.Fatal("MQTT metadata changed durable identity")
		}
		upstream = job.UpstreamID
	}
	if len(requests) != 0 {
		t.Fatal("leaked admission slot")
	}
}

func TestMQTTAdmissionRefusesBeforeJournal(t *testing.T) {
	for _, kind := range []string{"topic", "principal", "target", "signature", "capacity", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			d, s, identity, body, _ := oscoreRPCFixture(t)
			var frame FrameRequest
			if err := json.Unmarshal(body, &frame); err != nil {
				t.Fatal(err)
			}
			packet := mqttwire.Packet{Kind: mqttwire.Publish, ID: 1, Topic: "airc/send", Payload: frame.Envelope}
			route := config.MQTTRoute{Principal: identity.ID(), Target: core.Target{Kind: "room", Name: "#fixture"}}
			requests := make(chan struct{}, 1)
			ctx := t.Context()
			switch kind {
			case "topic":
				packet.Topic = "unknown"
			case "principal":
				route.Principal = "other"
			case "target":
				route.Target.Name = "#other"
			case "signature":
				packet.Payload = "bad.signature"
			case "capacity":
				requests <- struct{}{}
			case "cancelled":
				var cancel func()
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if admitMQTT(ctx, packet, identity, map[string]config.MQTTRoute{"airc/send": route}, d, requests) {
				t.Fatal("unsafe publication admitted")
			}
			if _, err := s.Lookup(t.Context(), identity, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal touched journal", err)
			}
			want := 0
			if kind == "capacity" {
				want = 1
			}
			if len(requests) != want {
				t.Fatal("refusal changed someone else's capacity")
			}
		})
	}
}
