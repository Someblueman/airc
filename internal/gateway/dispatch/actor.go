package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// actor is who a read, lookup or discovery runs as: a configured principal
// (the temporary shim) or a per-request author admitted by a listener gate.
type actor struct {
	identity auth.Identity
	author   *store.Submission
	gate     *gate
}

func principal(identity auth.Identity) actor { return actor{identity: identity} }

// access is a permission snapshot. For an author on a listener without a
// target allowlist, anyTarget admits every target and AIRC alone decides.
type access struct {
	store.Access
	anyTarget bool
}

func (a access) allows(target core.Target) bool {
	return a.anyTarget || slices.Contains(a.Targets, target)
}

// operationsFor is the ingress's enabled operation set for this actor.
func (d *Dispatcher) operationsFor(who actor) []core.Operation {
	if who.author != nil {
		return who.gate.operations
	}
	return d.operations
}

func (d *Dispatcher) enabled(who actor, op core.Operation) bool {
	return slices.Contains(d.operationsFor(who), op)
}

func (d *Dispatcher) permissions(ctx context.Context, who actor) (access, error) {
	if who.author == nil {
		a, err := d.journal.Permissions(ctx, who.identity)
		return access{Access: a}, err
	}
	g := who.gate
	targets := slices.Clone(g.targets)
	if targets == nil {
		targets = []core.Target{}
	}
	limits, retry := d.journal.Defaults()
	return access{Access: store.Access{Operations: slices.Clone(g.operations), Targets: targets, Limits: limits, Retry: retry}, anyTarget: g.targets == nil}, nil
}

func (d *Dispatcher) authorizeRead(ctx context.Context, who actor, r core.Request, now time.Time) (*store.ReadGrant, error) {
	if who.author == nil {
		return d.journal.AuthorizeRead(ctx, who.identity, r, now)
	}
	return d.journal.AuthorizeAuthorRead(ctx, *who.author, r, now)
}

// authorScope owns cursors: the author's own scope, never the listener's.
func (d *Dispatcher) authorScope(who actor) (string, error) {
	if who.author == nil {
		return d.journal.AuthorScope(who.identity)
	}
	return who.author.Author.Scope(), nil
}

func (who actor) listenerCapabilities(a access) *ListenerCapabilities {
	if who.author == nil {
		return nil
	}
	return &ListenerCapabilities{Guests: who.gate.guests, PasswordRequired: who.gate.password != nil, AnyTarget: a.anyTarget}
}
