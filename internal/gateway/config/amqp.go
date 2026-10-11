package config

import (
	"errors"
	"slices"

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
	if l.AMQP == nil || len(l.AMQP.Routes) < 1 || len(l.AMQP.Routes) > 128 || len(l.AllowedOrigins) != 0 {
		return errors.New("AMQP requires 1-128 fixed link address routes without origins")
	}
	for _, op := range l.Operations {
		if !amqpOperation(op) {
			return errors.New("unsupported AMQP operation")
		}
	}
	addresses := make(map[string]bool, 2*len(l.AMQP.Routes))
	for _, route := range l.AMQP.Routes {
		if !amqpAddress(route.RequestAddress) || !amqpAddress(route.ResponseAddress) || route.RequestAddress == route.ResponseAddress || addresses[route.RequestAddress] || addresses[route.ResponseAddress] {
			return errors.New("invalid or duplicate AMQP link address route")
		}
		addresses[route.RequestAddress], addresses[route.ResponseAddress] = true, true
		if operations(route.Operations) != nil {
			return errors.New("AMQP route requires distinct operations")
		}
		for _, op := range route.Operations {
			if !amqpOperation(op) || len(l.Operations) > 0 && !slices.Contains(l.Operations, op) {
				return errors.New("AMQP route operation exceeds the listener's")
			}
			if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
				return errors.New("AMQP history/search requires a cursor key file")
			}
		}
	}
	return nil
}

func amqpOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

// The fixed-address profile deliberately excludes wildcard and dynamic syntax.
func amqpAddress(address string) bool {
	if len(address) == 0 || len(address) > 255 {
		return false
	}
	for _, b := range []byte(address) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '/' || b == '-' || b == '_' || b == '.' || b == ':' {
			continue
		}
		return false
	}
	return true
}
