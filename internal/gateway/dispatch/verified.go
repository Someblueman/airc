package dispatch

import (
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// VerifiedMessage keeps authenticated intent private. Adapters can inspect
// routing facts but cannot construct, modify or transfer intent between ingress
// dispatchers. It is authorization, not a durable admission or native lease.
type VerifiedMessage struct {
	owner      *Dispatcher
	message    auth.AuthenticatedMessage
	verifiedAt int64
}

func (v VerifiedMessage) PrincipalID() string       { return v.message.Identity.ID() }
func (v VerifiedMessage) Binding() string           { return v.message.Identity.Binding() }
func (v VerifiedMessage) Operation() core.Operation { return v.message.Request.Operation }

// Target returns a copy of the authenticated destination, never mutable intent.
func (v VerifiedMessage) Target() (core.Target, bool) {
	if v.message.Request.Target == nil {
		return core.Target{}, false
	}
	return *v.message.Request.Target, true
}

func (d *Dispatcher) AuthenticateDetached(token string, encrypted bool, now time.Time) (VerifiedMessage, error) {
	if d == nil || d.verifier == nil {
		return VerifiedMessage{}, auth.ErrAuthentication
	}
	var message auth.AuthenticatedMessage
	var err error
	if encrypted {
		message, err = d.verifier.AuthenticateEncrypted(token, now)
	} else {
		message, err = d.verifier.AuthenticateSigned(token, now)
	}
	if err != nil {
		return VerifiedMessage{}, err
	}
	return VerifiedMessage{owner: d, message: message, verifiedAt: now.Unix()}, nil
}

func (v VerifiedMessage) forDispatcher(d *Dispatcher, now time.Time) (auth.AuthenticatedMessage, error) {
	if d == nil || v.owner != d || now.Unix() < v.verifiedAt || now.Unix() < 0 || now.Unix() > v.message.ReplayUntil {
		return auth.AuthenticatedMessage{}, auth.ErrAuthentication
	}
	return v.message, nil
}
