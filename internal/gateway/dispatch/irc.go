package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// IRC admits only proof belonging to this dispatcher's verifier. The journal
// performs current authorization, original event freshness and atomic replay intake.
func (d *Dispatcher) IRC(ctx context.Context, proof auth.VerifiedIRCEvent, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || !proof.Valid() || proof.IsRead() || !d.verifier.OwnsIdentity(proof.Identity()) {
		return core.Result{}, auth.ErrAuthentication
	}
	if !slices.Contains(d.operations, core.Send) {
		return core.Result{}, ErrUnsupported
	}
	job, _, err := d.journal.AdmitIRC(ctx, proof, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}

// IRCRead consumes immutable server-authenticated read parameters. The original
// event freshness bounds native work; no client tag can select another identity
// or target, and the normal durable read permissions are checked at execution.
func (d *Dispatcher) IRCRead(ctx context.Context, proof auth.VerifiedIRCEvent, now time.Time) (DetachedResult, error) {
	if d == nil || d.journal == nil || d.verifier == nil || !proof.Valid() || !proof.IsRead() || !d.verifier.OwnsIdentity(proof.Identity()) || now.Before(proof.Occurred()) || !now.Before(proof.ReplayDeadline()) {
		return DetachedResult{}, auth.ErrAuthentication
	}
	r, err := proof.ReadRequest()
	if err != nil || !slices.Contains(proof.Operations(), r.Operation) || !slices.Contains(d.operations, r.Operation) {
		return DetachedResult{}, auth.ErrForbidden
	}
	ctx, cancel := context.WithDeadline(ctx, proof.ReplayDeadline())
	defer cancel()
	result := DetachedResult{Version: 1, Operation: r.Operation}
	switch r.Operation {
	case core.ContextRead:
		value, e := d.readContext(ctx, principal(proof.Identity()), r, now)
		if e != nil {
			return DetachedResult{}, e
		}
		result.Context = &value
	case core.HistoryRead, core.Search:
		page, e := d.readPage(ctx, principal(proof.Identity()), r, "", now, nil)
		if e != nil {
			return DetachedResult{}, e
		}
		result.Page = &page
	default:
		return DetachedResult{}, ErrUnsupported
	}
	return result, nil
}
