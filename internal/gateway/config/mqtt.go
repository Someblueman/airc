package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

// MQTT grants literal topics to connection principals and exact AIRC targets.
// CONNECT bearer credentials remain in the existing principal config.
type MQTT struct {
	Routes []MQTTRoute `json:"routes"`
}

type MQTTRoute struct {
	Principal string      `json:"principal"`
	Topic     string      `json:"topic"`
	Target    core.Target `json:"target"`
}

func (c Config) validateMQTT(l Listener) error {
	if l.Protocol != "mqtt" {
		if l.MQTT != nil {
			return errors.New("MQTT options require mqtt protocol")
		}
		return nil
	}
	if l.MQTT == nil || len(l.MQTT.Routes) < 1 || len(l.MQTT.Routes) > 128 || l.TLSCertFile == "" || len(l.AllowedOrigins) != 0 || len(l.Operations) != 1 || l.Operations[0] != core.Send {
		return errors.New("MQTT requires TLS, 1-128 literal topic grants and only explicit send without origins")
	}
	topics := make(map[string]bool, len(l.MQTT.Routes))
	for _, route := range l.MQTT.Routes {
		if !id(route.Principal) || !mqttwire.ValidTopic(route.Topic) || topics[route.Topic] {
			return errors.New("invalid or duplicate MQTT topic grant")
		}
		topics[route.Topic] = true
		authorized := false
		for _, p := range c.Principals {
			if p.ID == route.Principal && !p.Revoked && p.BearerTokenFile != "" && slices.Contains(p.Operations, core.Send) && slices.Contains(p.Targets, route.Target) {
				authorized = true
				break
			}
		}
		if !authorized {
			return errors.New("MQTT topic grant requires an active bearer principal authorized for the exact target")
		}
	}
	return nil
}
