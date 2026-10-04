// Package dispatch is the authenticated ingress boundary. Adapters never
// contact AIRC directly; admitted mutations belong to the service workers.
package dispatch

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var ErrUnsupported = errors.New(string(core.UnsupportedOperation))

type Dispatcher struct {
	journal    *store.Store
	verifier   *auth.Verifier
	operations []core.Operation
}

func New(journal *store.Store, verifier *auth.Verifier, configured []core.Operation) (*Dispatcher, error) {
	if journal == nil || verifier == nil {
		return nil, store.ErrInvalid
	}
	for _, op := range configured {
		if !op.Valid() {
			return nil, store.ErrInvalid
		}
	}
	implemented := []core.Operation{core.Send, core.Reply, core.ReceiptGet}
	return &Dispatcher{journal, verifier, core.EffectiveOperations(implemented, configured, implemented, implemented)}, nil
}

// Session authenticates before parsing or performing journal work. Operations
// absent from this ingress's configured and implemented intersection never admit.
func (d *Dispatcher) Session(ctx context.Context, token string, body []byte, now time.Time) (core.Result, error) {
	return d.session(ctx, token, body, now, false)
}

// Mutation is the mutation-only session profile used by HTTP POST.
func (d *Dispatcher) Mutation(ctx context.Context, token string, body []byte, now time.Time) (core.Result, error) {
	return d.session(ctx, token, body, now, true)
}

func (d *Dispatcher) session(ctx context.Context, token string, body []byte, now time.Time, mutation bool) (core.Result, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.Result{}, err
	}
	r, err := core.Decode(body)
	if err != nil || mutation && r.Operation != core.Send && r.Operation != core.Reply {
		return core.Result{}, store.ErrInvalid
	}
	return d.execute(ctx, identity, r, 0, now)
}

func (d *Dispatcher) Signed(ctx context.Context, token string, now time.Time) (core.Result, error) {
	m, err := d.verifier.AuthenticateSigned(token, now)
	if err != nil {
		return core.Result{}, err
	}
	return d.execute(ctx, m.Identity, m.Request, m.ReplayUntil, now)
}

func (d *Dispatcher) Encrypted(ctx context.Context, token string, now time.Time) (core.Result, error) {
	m, err := d.verifier.AuthenticateEncrypted(token, now)
	if err != nil {
		return core.Result{}, err
	}
	return d.execute(ctx, m.Identity, m.Request, m.ReplayUntil, now)
}

func (d *Dispatcher) execute(ctx context.Context, identity auth.Identity, r core.Request, replayUntil int64, now time.Time) (core.Result, error) {
	if !slices.Contains(d.operations, r.Operation) {
		return core.Result{}, ErrUnsupported
	}
	if r.Operation == core.ReceiptGet {
		j, err := d.journal.Lookup(ctx, identity, r.LookupID)
		return j.Result, err
	}
	j, _, err := d.journal.Admit(ctx, identity, r, replayUntil, now)
	return j.Result, err
}
