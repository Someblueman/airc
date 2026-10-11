package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// GitHub admits a verified comment as its commenter's author (a mapped
// account or the guest "gh-<login>"). The journal performs original event
// freshness and atomic replay intake.
func (d *Dispatcher) GitHub(ctx context.Context, proof auth.VerifiedGitHubComment, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || !proof.Valid() {
		return core.Result{}, auth.ErrAuthentication
	}
	target := proof.Target()
	sub, err := d.routeActor(RouteSource(d.listener()), proof.Author(), core.Request{Version: 1, RequestID: proof.RequestID(), Operation: core.Send, Target: &target, Message: proof.Body()}, now)
	if err != nil {
		return core.Result{}, err
	}
	job, _, err := d.journal.AdmitGitHub(ctx, proof, sub, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}
