package config

import (
	"errors"
	"fmt"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// ListenerLimits are a listener's IRC-style flood and connection limits for
// author requests. Zero fields take the defaults below.
type ListenerLimits struct {
	// RequestsPerMinute is the per-source request rate (default 2400;
	// loopback sources are exempt, as in aircd).
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
	// AuthorRequestsPerMinute is the per-author request rate (default 1200).
	AuthorRequestsPerMinute int `json:"author_requests_per_minute,omitempty"`
	// Connections caps this listener's open connections (default 1024);
	// the global session limit still applies.
	Connections int `json:"connections,omitempty"`
	// ConnectionsPerSource caps one client address's open connections
	// (default 16). Loopback peers and carriers without addresses use only
	// Connections.
	ConnectionsPerSource int `json:"connections_per_source,omitempty"`
}

const (
	DefaultRequestsPerMinute       = 2400
	DefaultAuthorRequestsPerMinute = 1200
	DefaultConnections             = 1024
	DefaultConnectionsPerSource    = 16
)

// GuestsAllowed reports whether guest authors (a nickname without an account
// token) may use the listener. The default is true.
func (l Listener) GuestsAllowed() bool { return l.Guests == nil || *l.Guests }

// AccessLimits returns the listener's limits with defaults applied.
func (l Listener) AccessLimits() ListenerLimits {
	limits := ListenerLimits{}
	if l.Limits != nil {
		limits = *l.Limits
	}
	if limits.RequestsPerMinute == 0 {
		limits.RequestsPerMinute = DefaultRequestsPerMinute
	}
	if limits.AuthorRequestsPerMinute == 0 {
		limits.AuthorRequestsPerMinute = DefaultAuthorRequestsPerMinute
	}
	if limits.Connections == 0 {
		limits.Connections = DefaultConnections
	}
	if limits.ConnectionsPerSource == 0 {
		limits.ConnectionsPerSource = min(DefaultConnectionsPerSource, limits.Connections)
	}
	return limits
}

func validateListenerAccess(l *Listener) error {
	if !AuthorCarrier(l.Protocol) {
		if len(l.Targets) > 0 || l.Guests != nil || l.PasswordFile != "" || l.Limits != nil {
			return errors.New("author access settings require a listener that carries author requests")
		}
		return nil
	}
	if len(l.Targets) > 256 {
		return errors.New("listener target limit exceeded")
	}
	seen := map[core.Target]bool{}
	for i := range l.Targets {
		if err := l.Targets[i].Normalize(); err != nil {
			return fmt.Errorf("invalid target %q", l.Targets[i].Name)
		}
		if seen[l.Targets[i]] {
			return errors.New("duplicate listener target")
		}
		seen[l.Targets[i]] = true
	}
	if l.PasswordFile != "" && !absolute(l.PasswordFile) {
		return errors.New("invalid password file path")
	}
	if p := l.Limits; p != nil {
		if p.RequestsPerMinute < 0 || p.RequestsPerMinute > 100000 || p.AuthorRequestsPerMinute < 0 || p.AuthorRequestsPerMinute > 100000 || p.Connections < 0 || p.Connections > 4096 || p.ConnectionsPerSource < 0 || p.ConnectionsPerSource > 4096 {
			return errors.New("listener limits out of range")
		}
		if limits := l.AccessLimits(); limits.ConnectionsPerSource > limits.Connections {
			return errors.New("connections_per_source exceeds connections")
		}
	}
	return nil
}
