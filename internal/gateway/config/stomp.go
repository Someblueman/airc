package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// DefaultSTOMPRequestDestination and DefaultSTOMPResponseDestination are the route of
// an STOMP listener with no routes.
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
	if l.STOMP == nil || len(l.STOMP.Routes) < 1 || len(l.STOMP.Routes) > 128 || len(l.AllowedOrigins) != 0 {
		return errors.New("STOMP requires 1-128 fixed destination routes without origins")
	}
	for _, op := range l.Operations {
		if !stompOperation(op) {
			return errors.New("unsupported STOMP operation")
		}
	}
	addresses := make(map[string]bool, 2*len(l.STOMP.Routes))
	for _, route := range l.STOMP.Routes {
		if !stompDestination(route.RequestDestination) || !stompDestination(route.ResponseDestination) || route.RequestDestination == route.ResponseDestination || addresses[route.RequestDestination] || addresses[route.ResponseDestination] {
			return errors.New("invalid or duplicate STOMP destination route")
		}
		addresses[route.RequestDestination], addresses[route.ResponseDestination] = true, true
		if operations(route.Operations) != nil {
			return errors.New("STOMP route requires distinct operations")
		}
		for _, op := range route.Operations {
			if !stompOperation(op) || len(l.Operations) > 0 && !slices.Contains(l.Operations, op) {
				return errors.New("STOMP route operation exceeds the listener's")
			}
			if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
				return errors.New("STOMP history/search requires a cursor key file")
			}
		}
	}
	return nil
}

func stompOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

// The fixed-destination profile deliberately excludes wildcard and dynamic syntax.
func stompDestination(address string) bool {
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
