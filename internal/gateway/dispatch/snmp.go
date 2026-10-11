package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// SNMP admits a USM-verified notification as its grant's bound author. The
// journal performs engine timeliness and atomic replay intake.
func (d *Dispatcher) SNMP(ctx context.Context, proof auth.VerifiedSNMPNotification, now time.Time) (core.Result, error) {
	if d == nil || d.journal == nil || !proof.Valid() {
		return core.Result{}, auth.ErrAuthentication
	}
	target := proof.Target()
	sub, err := d.routeActor(RouteSource(d.listener()), proof.Author(), core.Request{Version: 1, RequestID: "snmp", Operation: core.Send, Target: &target, Message: proof.Body()}, now)
	if err != nil {
		return core.Result{}, err
	}
	job, _, err := d.journal.AdmitSNMP(ctx, proof, sub, now)
	if err != nil {
		return core.Result{}, err
	}
	return job.Result, nil
}
