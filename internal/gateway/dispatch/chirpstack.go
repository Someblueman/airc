package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// ChirpStack admits a verified uplink as its device's author (a mapped
// account or the guest "lora-<deveui>"). The journal atomically commits
// provider dedupe with intake.
func (d *Dispatcher) ChirpStack(ctx context.Context, proof auth.VerifiedChirpStackUplink, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || !proof.Valid() {
		return core.Result{}, auth.ErrAuthentication
	}
	target := proof.Target()
	sub, err := d.routeActor(RouteSource(d.listener()), proof.Author(), core.Request{Version: 1, RequestID: proof.RequestID(), Operation: core.Send, Target: &target, Message: proof.Body()}, now)
	if err != nil {
		return core.Result{}, err
	}
	job, _, err := d.journal.AdmitChirpStack(ctx, proof, sub, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}
