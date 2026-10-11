package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ForBridge constructs a chat bridge's dispatcher. A bridge is an author
// carrier whose authors come from provider-verified identities, so guests are
// always admitted here (the bridge itself decides whether unmapped users get
// a guest author) and the targets are exactly the routes' targets. Each
// external user is its own source, so the per-source limit is per user.
func ForBridge(journal *store.Store, reader *backend.Backend, cursors *auth.CursorCodec, id string, routes []config.BridgeRoute) (*Dispatcher, error) {
	var operations []core.Operation
	var targets []core.Target
	for _, r := range routes {
		for _, op := range r.Operations {
			if !slices.Contains(operations, op) {
				operations = append(operations, op)
			}
		}
		if !slices.Contains(targets, r.Target) {
			targets = append(targets, r.Target)
		}
	}
	d, err := newDispatcher(journal, reader, cursors, operations)
	if err != nil {
		return nil, err
	}
	g := &gate{listener: id, guests: true, targets: targets}
	for _, op := range operations {
		if (op == core.HistoryRead || op == core.Search) && cursors == nil {
			continue
		}
		g.operations = append(g.operations, op)
	}
	limits := config.Listener{}.AccessLimits()
	g.perSource = newBuckets(limits.RequestsPerMinute)
	g.perAuthor = newBuckets(limits.AuthorRequestsPerMinute)
	d.gate = g
	return d, nil
}

// Bridge admits one verified bridged message as its author: a send becomes a
// durable author job keyed by the provider message's request ID (a replay of
// the same message is the same job), and a read runs as the author. The
// author's WEBIRC source is the bridge's per-user label.
func (d *Dispatcher) Bridge(ctx context.Context, e auth.BridgeEvent, now time.Time) (DetachedResult, error) {
	if d == nil || d.gate == nil || d.journal == nil || !e.Valid() {
		return DetachedResult{}, auth.ErrAuthentication
	}
	if e.Refused() {
		return DetachedResult{}, store.ErrInvalid
	}
	if !e.Allowed() {
		return DetachedResult{}, ErrUnsupported
	}
	a := e.Author()
	r := e.Request()
	if !r.Operation.IsMutation() {
		return d.Admit(ctx, d.listener(), a.Source, a.Author, a.Credential, r, now)
	}
	who, r, err := d.admit(d.listener(), a.Source, a.Author, a.Credential, r, now)
	if err != nil {
		return DetachedResult{}, err
	}
	// The event's freshness window (at most five minutes) is far inside the
	// journal's terminal retention, so a replay inside it finds the job.
	job, _, err := d.journal.AdmitAuthor(ctx, *who.author, r, 0, now)
	if err != nil {
		return DetachedResult{}, err
	}
	return DetachedResult{Version: 1, Operation: r.Operation, Result: &job.Result}, nil
}
