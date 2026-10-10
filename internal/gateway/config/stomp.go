package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// STOMP provisions fixed request/response destinations. Login credentials belong
// to the existing principal, never to a STOMP destination; that principal's
// authenticated connection is the authority for every plain request on its route.
type STOMP struct {
	Routes []STOMPRoute `json:"routes"`
}

type STOMPRoute struct {
	Principal           string           `json:"principal"`
	RequestDestination  string           `json:"request_destination"`
	ResponseDestination string           `json:"response_destination"`
	Target              core.Target      `json:"target"`
	Operations          []core.Operation `json:"operations"`
}

func (c Config) validateSTOMP(l Listener) error {
	if l.Protocol != "stomp" {
		if l.STOMP != nil {
			return errors.New("STOMP options require stomp protocol")
		}
		return nil
	}
	if l.STOMP == nil || len(l.STOMP.Routes) < 1 || len(l.STOMP.Routes) > 128 || l.TLSCertFile == "" || len(l.AllowedOrigins) != 0 || len(l.Operations) == 0 {
		return errors.New("STOMP requires TLS, 1-128 fixed destination grants and explicit operations without origins")
	}
	for _, op := range l.Operations {
		if !stompOperation(op) {
			return errors.New("unsupported STOMP operation")
		}
	}
	addresses := make(map[string]bool, 2*len(l.STOMP.Routes))
	used := make(map[core.Operation]bool)
	for _, route := range l.STOMP.Routes {
		if !id(route.Principal) || !stompDestination(route.RequestDestination) || !stompDestination(route.ResponseDestination) || route.RequestDestination == route.ResponseDestination || addresses[route.RequestDestination] || addresses[route.ResponseDestination] {
			return errors.New("invalid or duplicate STOMP destination grant")
		}
		addresses[route.RequestDestination], addresses[route.ResponseDestination] = true, true
		if len(route.Operations) == 0 || operations(route.Operations) != nil {
			return errors.New("STOMP route requires distinct explicit operations")
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID != route.Principal || p.Revoked || p.BearerTokenFile == "" || !slices.Contains(p.Targets, route.Target) {
				continue
			}
			authorized = true
			for _, op := range route.Operations {
				if !stompOperation(op) || !slices.Contains(l.Operations, op) || !slices.Contains(p.Operations, op) {
					return errors.New("STOMP route operation exceeds listener or principal authority")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("STOMP history/search requires a cursor key file")
				}
				used[op] = true
			}
			break
		}
		if !authorized {
			return errors.New("STOMP route requires an active bearer principal authorized for the exact target")
		}
	}
	for _, op := range l.Operations {
		if !used[op] {
			return errors.New("STOMP listener operation requires an explicit route grant")
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
