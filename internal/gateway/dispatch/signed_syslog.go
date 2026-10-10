package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// SignedSyslog admits a cryptographically verified batch through the same
// configured-operation and verifier-provenance boundary as other ingress.
func (d *Dispatcher) SignedSyslog(ctx context.Context, batch *auth.VerifiedSyslogBatch, now time.Time) ([]core.Result, error) {
	if d == nil || d.journal == nil || !batch.Valid(now) || !d.verifier.OwnsIdentity(batch.Identity) {
		return nil, auth.ErrAuthentication
	}
	if !slices.Contains(d.operations, core.Send) {
		return nil, ErrUnsupported
	}
	jobs, err := d.journal.AdmitSignedSyslog(ctx, batch, now)
	if err != nil {
		return nil, err
	}
	results := make([]core.Result, len(jobs))
	for i, job := range jobs {
		results[i] = job.Result
	}
	return results, nil
}
