package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// SNMP admits only proof belonging to this dispatcher's verifier. The journal
// performs current authorization, engine timeliness and atomic replay intake.
func (d *Dispatcher) SNMP(ctx context.Context, proof auth.VerifiedSNMPNotification, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || !proof.Valid() || !d.verifier.OwnsIdentity(proof.Identity()) {
		return core.Result{}, auth.ErrAuthentication
	}
	if !slices.Contains(d.operations, core.Send) {
		return core.Result{}, ErrUnsupported
	}
	job, _, err := d.journal.AdmitSNMP(ctx, proof, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}
