package adapter

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/natswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type natsAdmission struct {
	requestID string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	refused   bool // No authenticated authorized response scope exists yet.
}

// admitNATS resolves authority from signed intent, never from PUB/reply subjects.
// A successful Send result must establish journal durability before the caller
// may publish its signed result. Protocol +OK alone is not durable acceptance.
func admitNATS(parent context.Context, d *dispatch.Dispatcher, identity auth.Identity, route config.NATSRoute, f natswire.Frame, requests chan struct{}) natsAdmission {
	if d == nil || cap(requests) < 1 || f.Command != "PUB" || len(f.Payload) == 0 || len(f.Payload) > natswire.MaxPayload || f.Subject != route.RequestSubject || f.Reply != route.ResponseSubject || f.Reply == "" || f.SID != "" || len(f.JSON) != 0 || f.Error != "" || f.MaxMessages != nil {
		return natsAdmission{refused: true}
	}
	verified, err := d.AuthenticateDetached(string(f.Payload), false, time.Now())
	if err != nil || verified.PrincipalID() != identity.ID() || verified.Binding() != identity.Binding() || route.Principal != identity.ID() {
		return natsAdmission{refused: true}
	}
	target, ok := verified.Target()
	if !ok || target != route.Target || !slices.Contains(route.Operations, verified.Operation()) || !natsRequestOperation(verified.Operation()) {
		return natsAdmission{refused: true}
	}
	a := natsAdmission{requestID: verified.RequestID()}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		a.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return a
	}
	ctx, cancel := context.WithTimeout(parent, natsDeadline)
	defer cancel()
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		status, code := httpError(err)
		a.failure = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		a.result = &result
	}
	return a
}

func natsRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
