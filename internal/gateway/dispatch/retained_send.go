package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// RetainedSignedSend proves an identical retained Send under current authority.
// It cannot admit intent, extend a replay marker, read AIRC or schedule delivery.
// Route/entry binding is supplied by the owner and matched before journal lookup.
func (d *Dispatcher) RetainedSignedSend(ctx context.Context, token, principal, requestID string, target core.Target, now time.Time) (DetachedResult, string, error) {
	if d == nil || d.verifier == nil || !slices.Contains(d.operations, core.Send) {
		return DetachedResult{}, "", ErrUnsupported
	}
	proof, err := d.verifier.VerifyRetainedSend(token, now)
	if err != nil {
		return DetachedResult{}, "", err
	}
	r := proof.Request
	if proof.Identity.ID() != principal || r.RequestID != requestID || r.Target == nil || *r.Target != target {
		return DetachedResult{}, "", auth.ErrForbidden
	}
	j, err := d.journal.LookupRetainedSend(ctx, proof)
	if err != nil {
		return DetachedResult{}, "", err
	}
	return DetachedResult{Version: 1, Operation: core.Send, Result: &j.Result}, proof.Identity.Binding(), nil
}
