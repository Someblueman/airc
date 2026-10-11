package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// DefaultSTOMPRequestDestination and DefaultSTOMPResponseDestination are the route of
// a STOMP listener with no routes.
const (
	DefaultSTOMPRequestDestination  = "airc.requests"
	DefaultSTOMPResponseDestination = "airc.responses"
)

// STOMP lists fixed request/response destinations. Each request is a plain
// contract request naming its author and target, as on every other carrier;
// the listener's targets allowlist, when set, bounds the rooms. A route's
// operations, when set, narrow the listener's. TLS is optional; the
// CONNECT passcode password is the listener access password, never an identity.
type STOMP struct {
	Routes []STOMPRoute `json:"routes,omitempty"`
}

type STOMPRoute struct {
	RequestDestination  string           `json:"request_destination"`
	ResponseDestination string           `json:"response_destination"`
	Operations          []core.Operation `json:"operations,omitempty"`
}

func (c Config) validateSTOMP(l Listener) error {
	if l.Protocol != "stomp" {
		if l.STOMP != nil {
			return errors.New("STOMP options require stomp protocol")
		}
		return nil
	}
	var routes []fixedRoute
	if l.STOMP != nil {
		for _, route := range l.STOMP.Routes {
			routes = append(routes, fixedRoute{route.RequestDestination, route.ResponseDestination, route.Operations})
		}
	}
	return c.validateFixedRoutes(l, "STOMP", "destination", routes)
}
