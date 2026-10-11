package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// AMQP provisions fixed request/response link addresses. Each request is a
// plain contract request naming its author; SASL PLAIN's password is the
// listener access password, never an identity. TLS and SASL are optional.
type AMQP struct {
	Routes []AMQPRoute `json:"routes"`
}

type AMQPRoute struct {
	RequestAddress  string           `json:"request_address"`
	ResponseAddress string           `json:"response_address"`
	Target          core.Target      `json:"target"`
	Operations      []core.Operation `json:"operations"`
}

func (c Config) validateAMQP(l Listener) error {
	if l.Protocol != "amqp" {
		if l.AMQP != nil {
			return errors.New("AMQP options require amqp protocol")
		}
		return nil
	}
	if l.AMQP == nil || len(l.AMQP.Routes) < 1 || len(l.AMQP.Routes) > 128 || len(l.AllowedOrigins) != 0 || len(l.Operations) == 0 {
		return errors.New("AMQP requires 1-128 fixed address routes and explicit operations without origins")
	}
	for _, op := range l.Operations {
		if !amqpOperation(op) {
			return errors.New("unsupported AMQP operation")
		}
	}
	addresses := make(map[string]bool, 2*len(l.AMQP.Routes))
	used := make(map[core.Operation]bool)
	for _, route := range l.AMQP.Routes {
		target := route.Target
		if target.Normalize() != nil || target != route.Target || !amqpAddress(route.RequestAddress) || !amqpAddress(route.ResponseAddress) || route.RequestAddress == route.ResponseAddress || addresses[route.RequestAddress] || addresses[route.ResponseAddress] {
			return errors.New("invalid or duplicate AMQP link address grant")
		}
		addresses[route.RequestAddress], addresses[route.ResponseAddress] = true, true
		if len(route.Operations) == 0 || operations(route.Operations) != nil {
			return errors.New("AMQP route requires distinct explicit operations")
		}
		for _, op := range route.Operations {
			if !amqpOperation(op) || !slices.Contains(l.Operations, op) {
				return errors.New("AMQP route operation exceeds the listener's")
			}
			if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
				return errors.New("AMQP history/search requires a cursor key file")
			}
			used[op] = true
		}
	}
	for _, op := range l.Operations {
		if !used[op] {
			return errors.New("AMQP listener operation requires an explicit route")
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
