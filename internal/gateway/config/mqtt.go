package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

// DefaultMQTTTopic is the request topic of an MQTT listener with no routes.
const DefaultMQTTTopic = "airc/requests"

// MQTT lists the literal topics an MQTT listener accepts requests on. A
// PUBLISH payload is a plain contract request naming its author and target,
// as on every other carrier; the listener's targets allowlist, when set,
// bounds the rooms. The CONNECT password, when the listener has a
// password_file, is the listener access password, never an identity.
// MQTT 3.1.1 publications carry only mutations, having no reply; MQTT 5
// carries any operation the listener allows, replying on a Response Topic.
type MQTT struct {
	Routes []MQTTRoute `json:"routes,omitempty"`
	// Sessions caps the MQTT 5 sessions the listener keeps, connected or
	// waiting to be resumed (1-16384, default 1024), and SessionsPerAuthor
	// those of one author (1-Sessions, default 16). Each session may hold
	// 256 KiB of unacknowledged messages, in memory and in its file.
	Sessions          int `json:"sessions,omitempty"`
	SessionsPerAuthor int `json:"sessions_per_author,omitempty"`
}

// MQTT 5 session caps when the listener names none.
const (
	DefaultMQTTSessions          = 1024
	DefaultMQTTSessionsPerAuthor = 16
	MaxMQTTSessions              = 16384
)

type MQTTRoute struct {
	Topic string `json:"topic"`
}

func (c Config) validateMQTT(l Listener) error {
	if l.MQTT == nil || len(l.MQTT.Routes) < 1 || len(l.MQTT.Routes) > 128 {
		return errors.New("MQTT requires 1-128 literal topic routes")
	}
	if l.MQTT.Sessions < 1 || l.MQTT.Sessions > MaxMQTTSessions || l.MQTT.SessionsPerAuthor < 1 || l.MQTT.SessionsPerAuthor > l.MQTT.Sessions {
		return errors.New("MQTT sessions must be 1-16384 and sessions_per_author 1-sessions")
	}
	if len(l.AllowedOrigins) != 0 && l.Protocol != "mqtt_websocket" {
		return errors.New("MQTT takes origins only over WebSocket")
	}
	topics := make(map[string]bool, len(l.MQTT.Routes))
	for _, route := range l.MQTT.Routes {
		if !mqttwire.ValidTopic(route.Topic) || topics[route.Topic] {
			return errors.New("invalid or duplicate MQTT topic route")
		}
		topics[route.Topic] = true
	}
	return nil
}

// mqttDefaults gives an MQTT listener without routes its default request
// route, so it needs only an address.
func mqttDefaults(l *Listener) {
	if l.MQTT == nil {
		l.MQTT = &MQTT{}
	}
	if len(l.MQTT.Routes) == 0 {
		l.MQTT.Routes = []MQTTRoute{{Topic: DefaultMQTTTopic}}
	}
	if l.MQTT.Sessions == 0 {
		l.MQTT.Sessions = DefaultMQTTSessions
	}
	if l.MQTT.SessionsPerAuthor == 0 {
		l.MQTT.SessionsPerAuthor = min(DefaultMQTTSessionsPerAuthor, l.MQTT.Sessions)
	}
}
