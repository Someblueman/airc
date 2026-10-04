// Package dispatch is the authenticated ingress boundary. Adapters never
// contact AIRC directly; admitted mutations belong to the service workers.
package dispatch

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var ErrUnsupported = errors.New(string(core.UnsupportedOperation))

type Dispatcher struct {
	journal    *store.Store
	verifier   *auth.Verifier
	operations []core.Operation
	backend    *backend.Backend
	cursors    *auth.CursorCodec
	realm      string
}

func New(journal *store.Store, verifier *auth.Verifier, reader *backend.Backend, cursors *auth.CursorCodec, configured []core.Operation) (*Dispatcher, error) {
	if journal == nil || verifier == nil {
		return nil, store.ErrInvalid
	}
	for _, op := range configured {
		if !op.Valid() {
			return nil, store.ErrInvalid
		}
		if (op == core.HistoryRead || op == core.Search || op == core.Watch) && (reader == nil || cursors == nil) {
			return nil, store.ErrInvalid
		}
		if (op == core.Directory || op == core.ContextRead || op == core.PinsRead || op == core.RoomRead || op == core.WaitingRead) && reader == nil {
			return nil, store.ErrInvalid
		}
	}
	if slices.Contains(configured, core.Directory) && reader == nil {
		return nil, store.ErrInvalid
	}
	implemented := ImplementedOperations()
	d := &Dispatcher{journal: journal, verifier: verifier, operations: core.EffectiveOperations(implemented, configured, implemented, implemented), backend: reader, cursors: cursors}
	if reader != nil {
		d.realm = reader.Realm()
	}
	return d, nil
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
	if err != nil || mutation && r.Operation != core.Send && r.Operation != core.Reply && r.Operation != core.ReactionAdd {
		return core.Result{}, store.ErrInvalid
	}
	return d.execute(ctx, identity, r, 0, now)
}

func (d *Dispatcher) execute(ctx context.Context, identity auth.Identity, r core.Request, replayUntil int64, now time.Time) (core.Result, error) {
	if !slices.Contains(d.operations, r.Operation) {
		return core.Result{}, ErrUnsupported
	}
	if r.Operation == core.ReceiptGet {
		j, err := d.journal.Lookup(ctx, identity, r.LookupID)
		return j.Result, err
	}
	if r.Operation == core.HistoryRead || r.Operation == core.Search || r.Operation == core.Watch || r.Operation == core.Directory || r.Operation == core.ContextRead || r.Operation == core.PinsRead || r.Operation == core.RoomRead || r.Operation == core.WaitingRead {
		return core.Result{}, ErrUnsupported
	}
	j, _, err := d.journal.Admit(ctx, identity, r, replayUntil, now)
	return j.Result, err
}

// ImplementedOperations is the current gateway intake set, not a transport-completeness claim.
func ImplementedOperations() []core.Operation {
	return []core.Operation{core.Send, core.Reply, core.ReactionAdd, core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.Directory}
}
