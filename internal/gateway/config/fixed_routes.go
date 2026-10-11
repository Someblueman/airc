package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// fixedRoute is one request/response pair of a fixed-route broker carrier
// (AMQP link addresses, STOMP destinations).
type fixedRoute struct {
	request, response string
	operations        []core.Operation
}

// validateFixedRoutes checks a broker listener's 1-128 routes: fixed,
// distinct addresses across every route, and operations that narrow the
// listener's. name is the carrier ("AMQP") and kind its address term ("link
// address"), for errors.
func (c Config) validateFixedRoutes(l Listener, name, kind string, routes []fixedRoute) error {
	if len(routes) < 1 || len(routes) > 128 || len(l.AllowedOrigins) != 0 {
		return errors.New(name + " requires 1-128 fixed " + kind + " routes without origins")
	}
	for _, op := range l.Operations {
		if !fixedRouteOperation(op) {
			return errors.New("unsupported " + name + " operation")
		}
	}
	addresses := make(map[string]bool, 2*len(routes))
	for _, route := range routes {
		if !fixedRouteAddress(route.request) || !fixedRouteAddress(route.response) || route.request == route.response || addresses[route.request] || addresses[route.response] {
			return errors.New("invalid or duplicate " + name + " " + kind + " route")
		}
		addresses[route.request], addresses[route.response] = true, true
		if operations(route.operations) != nil {
			return errors.New(name + " route requires distinct operations")
		}
		for _, op := range route.operations {
			if !fixedRouteOperation(op) || len(l.Operations) > 0 && !slices.Contains(l.Operations, op) {
				return errors.New(name + " route operation exceeds the listener's")
			}
			if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
				return errors.New(name + " history/search requires a cursor key file")
			}
		}
	}
	return nil
}

func fixedRouteOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

// fixedRouteAddress accepts a literal address: the fixed-address profile
// deliberately excludes wildcard and dynamic syntax.
func fixedRouteAddress(address string) bool {
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
