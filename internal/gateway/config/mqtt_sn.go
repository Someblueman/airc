package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// MQTTSN maps predefined topic IDs to exact targets. A publication names its
// author in its contract JSON; a UDP source address is only its rate source.
type MQTTSN struct {
	Routes []MQTTSNRoute `json:"routes"`
}

type MQTTSNRoute struct {
	// Principal is the legacy signed-envelope publisher; leave it empty for
	// plain author publications.
	Principal string      `json:"principal,omitempty"`
	TopicID   uint16      `json:"topic_id"`
	Target    core.Target `json:"target"`
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
	// A route without a principal maps its topic to a target for plain author
	// publications; a principal keeps the legacy signed-envelope shim.
	legacy := l.MQTTSN.Routes[0].Principal != ""
	if legacy && l.Enabled && len(c.RecipientKeys) == 0 {
		return errors.New("enabled MQTT-SN requires a gateway recipient key for encrypted publications")
	}
	for _, route := range l.MQTTSN.Routes {
		if (route.Principal != "") != legacy || legacy && !id(route.Principal) || route.TopicID == 0 || route.TopicID == 0xffff || topics[route.TopicID] {
			return errors.New("invalid or duplicate MQTT-SN topic grant")
		}
		topics[route.TopicID] = true
		if !legacy {
			target := route.Target
			if target.Normalize() != nil {
				return errors.New("invalid MQTT-SN topic target")
			}
			continue
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID == route.Principal && !p.Revoked && len(p.SigningKeys) > 0 && slices.Contains(p.Operations, core.Send) && slices.Contains(p.Targets, route.Target) {
				authorized = true
				break
			}
		}
		if !authorized {
			return errors.New("MQTT-SN topic grant requires an active signing principal authorized for the exact target")
		}
	}
	return nil
}
