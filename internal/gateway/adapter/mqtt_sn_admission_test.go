//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/mqttsnwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestMQTTSNAdmissionReplay(t *testing.T) {
	f := newMQTTSNFixture(t)
	packet := mqttsnwire.Publish{TopicID: 7, Payload: f.token(t, mqttSNRequest(), true, 0)}
	routes := map[uint16]config.MQTTSNRoute{7: {Principal: "sensor", Target: *mqttSNRequest().Target}}
	requests := make(chan struct{}, 1)
	var upstream string
	for _, duplicate := range []bool{false, true, false} {
		packet.Duplicate = duplicate
		if admitMQTTSN(t.Context(), packet, routes, f.d, requests) != mqttSNAdmitted {
			t.Fatal("valid intake refused")
		}
		job, err := f.s.Lookup(t.Context(), f.identity, "mqtt-sn-request")
		if err != nil || !job.Result.Durable || job.Result.SendAttempts != 0 || job.Request.Message != mqttSNRequest().Message || upstream != "" && upstream != job.UpstreamID {
			t.Fatal("wrong durable replay", job, err)
		}
		upstream = job.UpstreamID
	}
	if len(requests) != 0 {
		t.Fatal("leaked request slot")
	}
	conflict := mqttSNRequest()
	conflict.Message = "conflicting body"
	packet.Payload = f.token(t, conflict, true, 0)
	if _, err := f.d.AuthenticateDetached(packet.Payload, true, time.Now()); err != nil {
		t.Fatal("conflict witness was not authenticated", err)
	}
	if admitMQTTSN(t.Context(), packet, routes, f.d, requests) != mqttSNRefused {
		t.Fatal("conflicting signed request admitted")
	}
	job, err := f.s.Lookup(t.Context(), f.identity, conflict.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message != mqttSNRequest().Message {
		t.Fatal("conflict changed original durable intent", job, err)
	}
}

func TestMQTTSNAdmissionRefusals(t *testing.T) {
	for _, kind := range []string{"unknown", "principal", "target", "plaintext", "signature", "expired", "future", "read", "capacity", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f := newMQTTSNFixture(t)
			request := mqttSNRequest()
			route := config.MQTTSNRoute{Principal: "sensor", Target: *request.Target}
			packet := mqttsnwire.Publish{TopicID: 7, Payload: f.token(t, request, true, 0)}
			requests := make(chan struct{}, 1)
			ctx, want := t.Context(), mqttSNRefused
			switch kind {
			case "unknown":
				packet.TopicID, want = 8, mqttSNUnknownTopic
			case "principal":
				route.Principal = "other"
			case "target":
				route.Target.Name = "#other"
			case "plaintext":
				packet.Payload = f.token(t, request, false, 0)
			case "signature":
				packet.Payload = "invalid"
			case "expired":
				packet.Payload = f.token(t, request, true, -600)
			case "future":
				packet.Payload = f.token(t, request, true, 600)
			case "read":
				request.Operation, request.Target, request.Message, request.LookupID = core.ReceiptGet, nil, "", "original"
				packet.Payload = f.token(t, request, true, 0)
			case "capacity":
				requests <- struct{}{}
			case "cancelled":
				var cancel func()
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if kind == "principal" || kind == "target" || kind == "unknown" || kind == "read" {
				if _, err := f.d.AuthenticateDetached(packet.Payload, true, time.Now()); err != nil {
					t.Fatal("refusal witness was not authenticated", err)
				}
			}
			if got := admitMQTTSN(ctx, packet, map[uint16]config.MQTTSNRoute{7: route}, f.d, requests); got != want {
				t.Fatalf("got %v want %v", got, want)
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal touched journal", err)
			}
			wantSlots := 0
			if kind == "capacity" {
				wantSlots = 1
			}
			if len(requests) != wantSlots {
				t.Fatal("changed slot ownership")
			}
		})
	}
}
