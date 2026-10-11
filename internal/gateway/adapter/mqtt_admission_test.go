package adapter

import (
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

// TestMQTTAdmissionRefusesUnroutedTopic covers topic routing; the refusals
// every carrier shares are in TestCarrierAdmission.
func TestMQTTAdmissionRefusesUnroutedTopic(t *testing.T) {
	f := newRouteFixture(t)
	d := f.listener(t, mqttFixtureListener())
	in := dispatch.Ingress{Source: "192.0.2.1", Session: dispatch.NewSession()}
	packet := mqttwire.Packet{Kind: mqttwire.Publish, QoS: 1, ID: 1, Topic: "unknown", Payload: plainRouteSend}
	requests := make(chan struct{}, 1)
	if admitMQTT(t.Context(), packet, in, mqttFixtureRoutes(), d, requests) {
		t.Fatal("unrouted publication admitted")
	}
	if f.jobs(t, "plain-route") != 0 || len(requests) != 0 {
		t.Fatal("refusal touched journal or capacity")
	}
}
