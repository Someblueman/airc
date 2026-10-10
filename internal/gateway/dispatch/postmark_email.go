package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// PostmarkEmail requires both an authenticated provider route and the author's
// encrypted signed intent. Email addresses and provider IDs cannot choose native
// request identity or renew the original envelope's validity.
func (d *Dispatcher) PostmarkEmail(ctx context.Context, proof auth.VerifiedPostmarkEmail, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || d.verifier == nil || !proof.Valid() || !d.verifier.OwnsIdentity(proof.Identity()) {
		return core.Result{}, auth.ErrAuthentication
	}
	if !slices.Contains(d.operations, core.Send) {
		return core.Result{}, ErrUnsupported
	}
	verified, err := d.AuthenticateDetached(proof.Token(), true, now)
	if err != nil {
		// Redelivery may acknowledge an already retained exact Send after
		// expiry, but this lookup cannot create or re-execute an old intent.
		result, binding, lookupErr := d.RetainedEncryptedSend(ctx, proof.Token(), proof.Identity().ID(), proof.Target(), now)
		if lookupErr == nil && binding == proof.Identity().Binding() && result.Result != nil && result.Result.Durable {
			return *result.Result, nil
		}
		return core.Result{}, err
	}
	target, ok := verified.Target()
	if !ok || target != proof.Target() || verified.PrincipalID() != proof.Identity().ID() || verified.Binding() != proof.Identity().Binding() || verified.Operation() != core.Send {
		return core.Result{}, auth.ErrForbidden
	}
	result, err := d.DetachedVerified(ctx, verified, now)
	if err != nil {
		return core.Result{}, err
	}
	if result.Result == nil || !result.Result.Durable || result.Result.RequestID != verified.RequestID() {
		return core.Result{}, store.ErrInvalid
	}
	return *result.Result, nil
}
