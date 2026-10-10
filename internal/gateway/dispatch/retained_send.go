package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// RetainedEncryptedSend recovers an identical already-journaled Send after a
// queue redelivery, including expiry. Queue metadata grants no authority and
// this path never admits or schedules work. The request ID comes from the proof.
func (d *Dispatcher) RetainedEncryptedSend(ctx context.Context, token, principal string, target core.Target, now time.Time) (DetachedResult, string, error) {
	if d == nil || d.verifier == nil || !slices.Contains(d.operations, core.Send) {
		return DetachedResult{}, "", ErrUnsupported
	}
	proof, err := d.verifier.VerifyRetainedEncryptedSend(token, now)
	if err != nil {
		return DetachedResult{}, "", err
	}
	return d.retainedSend(ctx, proof, principal, target)
}

func (d *Dispatcher) retainedSend(ctx context.Context, proof auth.RetainedSend, principal string, target core.Target) (DetachedResult, string, error) {
	r := proof.Request
	if proof.Identity.ID() != principal || r.Target == nil || *r.Target != target {
		return DetachedResult{}, "", auth.ErrForbidden
	}
	j, err := d.journal.LookupRetainedSend(ctx, proof)
	if err != nil {
		return DetachedResult{}, "", err
	}
	return DetachedResult{Version: 1, Operation: core.Send, Result: &j.Result}, proof.Identity.Binding(), nil
}
