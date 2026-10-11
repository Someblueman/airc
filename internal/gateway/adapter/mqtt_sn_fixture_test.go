//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func newMQTTSNFixture(t *testing.T) plainFixture {
	t.Helper()
	return newPlainFixture(t, mqttSNFixtureListener())
}

func mqttSNFixtureListener() config.Listener {
	return config.Listener{ID: "mqtt-sn", Protocol: "mqtt_sn", Operations: []core.Operation{core.Send}, MQTTSN: &config.MQTTSN{Routes: []config.MQTTSNRoute{mqttSNRoute()}}}
}

func mqttSNRoute() config.MQTTSNRoute {
	return config.MQTTSNRoute{TopicID: 7, Target: *mqttSNRequest().Target}
}

func mqttSNRequest() core.Request {
	return core.Request{Version: 1, RequestID: "mqtt-sn-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}
