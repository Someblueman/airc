//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/mqttsnwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var mqttSNSource = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}

func TestMQTTSNAdmissionReplay(t *testing.T) {
	f := newMQTTSNFixture(t)
	packet := mqttsnwire.Publish{TopicID: 7, Payload: f.body(t, mqttSNRequest())}
	routes := map[uint16]config.MQTTSNRoute{7: mqttSNRoute()}
	requests := make(chan struct{}, 1)
	var upstream string
	for _, duplicate := range []bool{false, true, false} {
		packet.Duplicate = duplicate
		if admitAuthorMQTTSN(t.Context(), packet, routes, f.d, requests, mqttSNSource) != mqttSNAdmitted {
			t.Fatal("valid intake refused")
		}
		job, err := f.lookup(t, "mqtt-sn-request")
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
	packet.Payload = f.body(t, conflict)
	if admitAuthorMQTTSN(t.Context(), packet, routes, f.d, requests, mqttSNSource) != mqttSNRefused {
		t.Fatal("conflicting request admitted")
	}
	job, err := f.lookup(t, conflict.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message != mqttSNRequest().Message {
		t.Fatal("conflict changed original durable intent", job, err)
	}
}

func TestMQTTSNAdmissionRefusals(t *testing.T) {
	for _, kind := range []string{"unknown", "target", "malformed", "read", "capacity", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f := newMQTTSNFixture(t)
			request := mqttSNRequest()
			route := mqttSNRoute()
			packet := mqttsnwire.Publish{TopicID: 7, Payload: f.body(t, request)}
			requests := make(chan struct{}, 1)
			ctx, want := t.Context(), mqttSNRefused
			switch kind {
			case "unknown":
				packet.TopicID, want = 8, mqttSNUnknownTopic
			case "target":
				route.Target.Name = "#other"
			case "malformed":
				packet.Payload = "invalid"
			case "read":
				request.Operation, request.Target, request.Message, request.LookupID = core.ReceiptGet, nil, "", "original"
				packet.Payload = f.body(t, request)
			case "capacity":
				requests <- struct{}{}
			case "cancelled":
				var cancel func()
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if got := admitAuthorMQTTSN(ctx, packet, map[uint16]config.MQTTSNRoute{7: route}, f.d, requests, mqttSNSource); got != want {
				t.Fatalf("got %v want %v", got, want)
			}
			if _, err := f.lookup(t, request.RequestID); !errors.Is(err, store.ErrNotFound) {
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
