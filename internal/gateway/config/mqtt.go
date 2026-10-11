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
type MQTT struct {
	Routes []MQTTRoute `json:"routes,omitempty"`
}

type MQTTRoute struct {
	Topic string `json:"topic"`
}

func (c Config) validateMQTT(l Listener) error {
	if l.Protocol != "mqtt" {
		if l.MQTT != nil {
			return errors.New("MQTT options require mqtt protocol")
		}
		return nil
	}
	if l.MQTT == nil || len(l.MQTT.Routes) < 1 || len(l.MQTT.Routes) > 128 || len(l.AllowedOrigins) != 0 {
		return errors.New("MQTT requires 1-128 literal topic routes without origins")
	}
	for _, op := range l.Operations {
		if !op.IsMutation() {
			return errors.New("MQTT carries only mutations: a publish has no reply")
		}
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

// applyBrokerDefaults gives an MQTT, AMQP or STOMP listener without routes
// its default request route, so the listener needs only an address.
func applyBrokerDefaults(l *Listener) {
	switch l.Protocol {
	case "mqtt":
		mqttDefaults(l)
	case "amqp":
		amqpDefaults(l)
	case "stomp":
		stompDefaults(l)
	}
}

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
