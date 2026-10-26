package dispatch

import (
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
)

// ListenerID is the listener this dispatcher admits for.
func (d *Dispatcher) ListenerID() string { return d.listener() }

// Connect applies a listener's access password to a new connection, as IRC's
// PASS before registration, for carriers whose login exchange carries it (an
// MQTT CONNECT password). It is never an identity. On a listener without a
// password every connection is admitted.
func (d *Dispatcher) Connect(s *Session, password core.Password) error {
	if d == nil || d.gate == nil || s == nil {
		return ErrUnsupported
	}
	if g := d.gate; g.password != nil && !password.Matches(*g.password) {
		return auth.ErrForbidden
	}
	s.admit()
	return nil
}

// PasswordRequired reports whether the listener has an access password.
func (d *Dispatcher) PasswordRequired() bool {
	return d != nil && d.gate != nil && d.gate.password != nil
}
