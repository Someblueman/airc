package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// readCheck authorizes a check on every one of its rooms, as room reads are
// authorized one room at a time, then reads them all on one session.
func (d *Dispatcher) readCheck(ctx context.Context, who actor, r core.Request, now time.Time) (core.CheckResult, error) {
	scope, err := d.checkScope(ctx, who, r)
	if err != nil {
		return core.CheckResult{}, err
	}
	grant, err := d.authorizeRead(ctx, who, r, now)
	if err != nil {
		return core.CheckResult{}, err
	}
	return d.backend.Check(ctx, grant, scope)
}

// followCheck is readCheck as a stream: results go to emit until the follow
// lease ends.
func (d *Dispatcher) followCheck(ctx context.Context, who actor, r core.Request, now time.Time, emit func(core.CheckResult) error) error {
	scope, err := d.checkScope(ctx, who, r)
	if err != nil {
		return err
	}
	grant, err := d.authorizeRead(ctx, who, r, now)
	if err != nil {
		return err
	}
	return d.backend.Follow(ctx, grant, scope, emit)
}

// checkScope authorizes a check on every one of its rooms and says what it
// may show beyond them.
func (d *Dispatcher) checkScope(ctx context.Context, who actor, r core.Request) (backend.CheckScope, error) {
	if !d.enabled(who, core.Check) {
		return backend.CheckScope{}, ErrUnsupported
	}
	access, err := d.permissions(ctx, who)
	if err != nil {
		return backend.CheckScope{}, err
	}
	if !slices.Contains(access.Operations, core.Check) {
		return backend.CheckScope{}, auth.ErrForbidden
	}
	for _, room := range r.Rooms {
		if !access.allows(core.Target{Kind: "room", Name: room.Name}) {
			return backend.CheckScope{}, auth.ErrForbidden
		}
	}
	// Only AIRC's own record proves who received a direct message, and it
	// records only accounts: a guest's inbox, and one on a listener whose
	// target allowlist leaves out the author's own nickname, has mentions
	// only. Threads and mentions show only rooms the listener allows.
	author := who.author.Author
	own := core.Target{Kind: "direct", Name: author.Nick}
	return backend.CheckScope{
		Readable:       func(room string) bool { return access.allows(core.Target{Kind: "room", Name: room}) },
		DirectMessages: author.Registered && own.Normalize() == nil && access.allows(own),
	}, nil
}
