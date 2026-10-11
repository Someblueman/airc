package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// MQTTSN maps predefined topic IDs to exact targets. A publication names its
// author in its contract JSON; a UDP source address is only its rate source.
type MQTTSN struct {
	Routes []MQTTSNRoute `json:"routes"`
}

type MQTTSNRoute struct {
	TopicID uint16      `json:"topic_id"`
	Target  core.Target `json:"target"`
}

func (c Config) validateMQTTSN(l Listener) error {
	if l.Protocol != "mqtt_sn" {
		if l.MQTTSN != nil {
			return errors.New("mqtt_sn options require mqtt_sn protocol")
		}
		return nil
	}
	if l.MQTTSN == nil || len(l.MQTTSN.Routes) < 1 || len(l.MQTTSN.Routes) > 128 || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("MQTT-SN requires 1-128 predefined topic grants and only explicit send without TLS or origins")
	}
	topics := make(map[uint16]bool, len(l.MQTTSN.Routes))
	for _, route := range l.MQTTSN.Routes {
		target := route.Target
		if route.TopicID == 0 || route.TopicID == 0xffff || topics[route.TopicID] || target.Normalize() != nil {
			return errors.New("invalid or duplicate MQTT-SN topic grant")
		}
		topics[route.TopicID] = true
	}
	return nil
}
