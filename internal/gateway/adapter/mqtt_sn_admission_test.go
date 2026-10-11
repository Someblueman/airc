//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/mqttsnwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var mqttSNSource = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}

// TestMQTTSNAdmissionTopicRefusals covers predefined-topic routing; the
// refusals every carrier shares are in TestCarrierAdmission.
func TestMQTTSNAdmissionTopicRefusals(t *testing.T) {
	for _, kind := range []string{"unknown", "target"} {
		t.Run(kind, func(t *testing.T) {
			f := newMQTTSNFixture(t)
			request := mqttSNRequest()
			route := mqttSNRoute()
			packet := mqttsnwire.Publish{TopicID: 7, Payload: f.body(t, request)}
			requests := make(chan struct{}, 1)
			want := mqttSNRefused
			switch kind {
			case "unknown":
				packet.TopicID, want = 8, mqttSNUnknownTopic
			case "target":
				route.Target.Name = "#other"
			}
			if got := admitAuthorMQTTSN(t.Context(), packet, map[uint16]config.MQTTSNRoute{7: route}, f.d, requests, mqttSNSource); got != want {
				t.Fatalf("got %v want %v", got, want)
			}
			if _, err := f.lookup(t, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal touched journal", err)
			}
			if len(requests) != 0 {
				t.Fatal("changed slot ownership")
			}
		})
	}
}
