package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// ChirpStack admits only a proof owned by this dispatcher's verifier. The
// journal rechecks current authority and atomically commits dedupe with intake.
func (d *Dispatcher) ChirpStack(ctx context.Context, proof auth.VerifiedChirpStackUplink, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || d.verifier == nil || !proof.Valid() || !d.verifier.OwnsIdentity(proof.Identity()) {
		return core.Result{}, auth.ErrAuthentication
	}
	if !slices.Contains(d.operations, core.Send) {
		return core.Result{}, ErrUnsupported
	}
	job, _, err := d.journal.AdmitChirpStack(ctx, proof, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}
