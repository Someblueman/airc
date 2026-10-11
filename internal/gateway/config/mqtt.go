package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

// MQTT maps literal topics to exact AIRC targets. A PUBLISH payload is a
// plain contract request naming its author; the CONNECT password, when the
// listener has a password_file, is the listener access password, never an
// identity.
type MQTT struct {
	Routes []MQTTRoute `json:"routes"`
}

type MQTTRoute struct {
	Topic  string      `json:"topic"`
	Target core.Target `json:"target"`
}

func (c Config) validateMQTT(l Listener) error {
	if l.Protocol != "mqtt" {
		if l.MQTT != nil {
			return errors.New("MQTT options require mqtt protocol")
		}
		return nil
	}
	if l.MQTT == nil || len(l.MQTT.Routes) < 1 || len(l.MQTT.Routes) > 128 || len(l.AllowedOrigins) != 0 || len(l.Operations) == 0 {
		return errors.New("MQTT requires 1-128 literal topic routes and explicit mutation operations without origins")
	}
	for _, op := range l.Operations {
		if !op.IsMutation() {
			return errors.New("MQTT carries only mutations: a publish has no reply")
		}
	}
	topics := make(map[string]bool, len(l.MQTT.Routes))
	for _, route := range l.MQTT.Routes {
		target := route.Target
		if !mqttwire.ValidTopic(route.Topic) || topics[route.Topic] || target.Normalize() != nil || target != route.Target {
			return errors.New("invalid or duplicate MQTT topic route")
		}
		topics[route.Topic] = true
	}
	return nil
}
