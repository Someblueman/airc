package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// AMQP provisions fixed request/response links. SASL credentials and envelope
// signing keys belong to the existing principal, never to an AMQP address.
type AMQP struct {
	Routes []AMQPRoute `json:"routes"`
}

type AMQPRoute struct {
	Principal       string           `json:"principal"`
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
	if l.AMQP == nil || len(l.AMQP.Routes) < 1 || len(l.AMQP.Routes) > 128 || l.TLSCertFile == "" || len(l.AllowedOrigins) != 0 || len(l.Operations) == 0 {
		return errors.New("AMQP requires TLS, 1-128 fixed address grants and explicit operations without origins")
	}
	for _, op := range l.Operations {
		if !amqpOperation(op) {
			return errors.New("unsupported AMQP operation")
		}
	}
	addresses := make(map[string]bool, 2*len(l.AMQP.Routes))
	used := make(map[core.Operation]bool)
	for _, route := range l.AMQP.Routes {
		if !id(route.Principal) || !amqpAddress(route.RequestAddress) || !amqpAddress(route.ResponseAddress) || route.RequestAddress == route.ResponseAddress || addresses[route.RequestAddress] || addresses[route.ResponseAddress] {
			return errors.New("invalid or duplicate AMQP link address grant")
		}
		addresses[route.RequestAddress], addresses[route.ResponseAddress] = true, true
		if len(route.Operations) == 0 || operations(route.Operations) != nil {
			return errors.New("AMQP route requires distinct explicit operations")
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID != route.Principal || p.Revoked || p.BearerTokenFile == "" || len(p.SigningKeys) == 0 || !slices.Contains(p.Targets, route.Target) {
				continue
			}
			authorized = true
			for _, op := range route.Operations {
				if !amqpOperation(op) || !slices.Contains(l.Operations, op) || !slices.Contains(p.Operations, op) {
					return errors.New("AMQP route operation exceeds listener or principal authority")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("AMQP history/search requires a cursor key file")
				}
				used[op] = true
			}
			break
		}
		if !authorized {
			return errors.New("AMQP route requires an active bearer and signing principal authorized for the exact target")
		}
	}
	for _, op := range l.Operations {
		if !used[op] {
			return errors.New("AMQP listener operation requires an explicit route grant")
		}
	}
	if c.ResponseSigningKey == nil {
		return errors.New("AMQP response links require a response signing key")
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
