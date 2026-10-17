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
}

type MQTTRoute struct {
	Topic string `json:"topic"`
}

func (c Config) validateMQTT(l Listener) error {
	if l.MQTT == nil || len(l.MQTT.Routes) < 1 || len(l.MQTT.Routes) > 128 {
		return errors.New("MQTT requires 1-128 literal topic routes")
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

// mqttDefaults, amqpDefaults and stompDefaults give a broker listener
// without routes its default request route, so it needs only an address.
func mqttDefaults(l *Listener) {
	if l.MQTT == nil {
		l.MQTT = &MQTT{}
	}
	if len(l.MQTT.Routes) == 0 {
		l.MQTT.Routes = []MQTTRoute{{Topic: DefaultMQTTTopic}}
	}
}

func amqpDefaults(l *Listener) {
	if l.AMQP == nil {
		l.AMQP = &AMQP{}
	}
	if len(l.AMQP.Routes) == 0 {
		l.AMQP.Routes = []AMQPRoute{{RequestAddress: DefaultAMQPRequestAddress, ResponseAddress: DefaultAMQPResponseAddress}}
	}
}

func stompDefaults(l *Listener) {
	if l.STOMP == nil {
		l.STOMP = &STOMP{}
	}
	if len(l.STOMP.Routes) == 0 {
		l.STOMP.Routes = []STOMPRoute{{RequestDestination: DefaultSTOMPRequestDestination, ResponseDestination: DefaultSTOMPResponseDestination}}
	}
}
