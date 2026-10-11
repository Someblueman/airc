package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// DefaultAMQPRequestAddress and DefaultAMQPResponseAddress are the route of
// an AMQP listener with no routes.
const (
	DefaultAMQPRequestAddress  = "airc.requests"
	DefaultAMQPResponseAddress = "airc.responses"
)

// AMQP lists fixed request/response link addresses. Each request is a plain
// contract request naming its author and target, as on every other carrier;
// the listener's targets allowlist, when set, bounds the rooms. A route's
// operations, when set, narrow the listener's. TLS is optional; the
// SASL PLAIN password is the listener access password, never an identity.
type AMQP struct {
	Routes []AMQPRoute `json:"routes,omitempty"`
}

type AMQPRoute struct {
	RequestAddress  string           `json:"request_address"`
	ResponseAddress string           `json:"response_address"`
	Operations      []core.Operation `json:"operations,omitempty"`
}

func (c Config) validateAMQP(l Listener) error {
	if l.Protocol != "amqp" {
		if l.AMQP != nil {
			return errors.New("AMQP options require amqp protocol")
		}
		return nil
	}
	var routes []fixedRoute
	if l.AMQP != nil {
		for _, route := range l.AMQP.Routes {
			routes = append(routes, fixedRoute{route.RequestAddress, route.ResponseAddress, route.Operations})
		}
	}
	return c.validateFixedRoutes(l, "AMQP", "link address", routes)
}
