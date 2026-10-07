package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

// NATS grants fixed request/reply subjects to existing authenticated principals.
type NATS struct {
	Routes []NATSRoute `json:"routes"`
}

type NATSRoute struct {
	Principal       string           `json:"principal"`
	RequestSubject  string           `json:"request_subject"`
	ResponseSubject string           `json:"response_subject"`
	Target          core.Target      `json:"target"`
	Operations      []core.Operation `json:"operations"`
}

func (c Config) validateNATS(l Listener) error {
	if l.Protocol != "nats" {
		if l.NATS != nil {
			return errors.New("NATS options require nats protocol")
		}
		return nil
	}
	if l.NATS == nil || len(l.NATS.Routes) < 1 || len(l.NATS.Routes) > 128 || l.TLSCertFile == "" || len(l.AllowedOrigins) != 0 || len(l.Operations) == 0 {
		return errors.New("NATS requires TLS, 1-128 fixed subject grants and explicit operations without origins")
	}
	for _, op := range l.Operations {
		if !natsOperation(op) {
			return errors.New("unsupported NATS operation")
		}
	}
	subjects := make(map[string]bool, 2*len(l.NATS.Routes))
	used := make(map[core.Operation]bool)
	for _, route := range l.NATS.Routes {
		if !id(route.Principal) || !natswire.Subject(route.RequestSubject) || !natswire.Subject(route.ResponseSubject) || route.RequestSubject == route.ResponseSubject || subjects[route.RequestSubject] || subjects[route.ResponseSubject] {
			return errors.New("invalid or duplicate NATS subject grant")
		}
		subjects[route.RequestSubject], subjects[route.ResponseSubject] = true, true
		if len(route.Operations) == 0 || operations(route.Operations) != nil {
			return errors.New("NATS route requires distinct explicit operations")
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID != route.Principal || p.Revoked || p.BearerTokenFile == "" || len(p.SigningKeys) == 0 || !slices.Contains(p.Targets, route.Target) {
				continue
			}
			authorized = true
			for _, op := range route.Operations {
				if !natsOperation(op) || !slices.Contains(l.Operations, op) || !slices.Contains(p.Operations, op) {
					return errors.New("NATS route operation exceeds listener or principal authority")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("NATS history/search requires a cursor key file")
				}
				used[op] = true
			}
			break
		}
		if !authorized {
			return errors.New("NATS route requires an active bearer and signing principal authorized for the exact target")
		}
	}
	for _, op := range l.Operations {
		if !used[op] {
			return errors.New("NATS listener operation requires an explicit route grant")
		}
	}
	if c.ResponseSigningKey == nil {
		return errors.New("NATS responses require a response signing key")
	}
	return nil
}

func natsOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
