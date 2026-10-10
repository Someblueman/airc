package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// Matrix admits only proof belonging to this dispatcher's verifier. The journal
// performs current authorization, original event freshness and atomic replay intake.
func (d *Dispatcher) Matrix(ctx context.Context, proof auth.VerifiedMatrixMessage, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || d.verifier == nil || !proof.Valid() || proof.IsRead() || !d.verifier.OwnsIdentity(proof.Identity()) {
		return core.Result{}, auth.ErrAuthentication
	}
	if !slices.Contains(d.operations, core.Send) {
		return core.Result{}, ErrUnsupported
	}
	job, _, err := d.journal.AdmitMatrix(ctx, proof, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}

// MatrixRead executes immutable event-authenticated parameters under the
// original event deadline and the journal's current durable read grants.
func (d *Dispatcher) MatrixRead(ctx context.Context, proof auth.VerifiedMatrixMessage, now time.Time) (DetachedResult, error) {
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
		value, err := d.readContext(ctx, proof.Identity(), r, now)
		if err != nil {
			return DetachedResult{}, err
		}
		result.Context = &value
	case core.HistoryRead, core.Search:
		page, err := d.readPage(ctx, proof.Identity(), r, "", now, nil)
		if err != nil {
			return DetachedResult{}, err
		}
		result.Page = &page
	default:
		return DetachedResult{}, ErrUnsupported
	}
	return result, nil
}
