package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// SignedSyslog admits a cryptographically verified batch as its origin's
// bound author, under the ingress's operation and flood limits.
func (d *Dispatcher) SignedSyslog(ctx context.Context, batch *auth.VerifiedSyslogBatch, now time.Time) ([]core.Result, error) {
	if d == nil || d.journal == nil || !batch.Valid(now) {
		return nil, auth.ErrAuthentication
	}
	target := batch.Target
	sub, err := d.routeActor(RouteSource(d.listener()), batch.Author, core.Request{Version: 1, RequestID: "syslog", Operation: core.Send, Target: &target, Message: "batch"}, now)
	if err != nil {
		return nil, err
	}
	jobs, err := d.journal.AdmitSignedSyslog(ctx, batch, sub, now)
	if err != nil {
		return nil, err
	}
	results := make([]core.Result, len(jobs))
	for i, job := range jobs {
		results[i] = job.Result
	}
	return results, nil
}
