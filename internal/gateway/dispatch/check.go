package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// readCheck authorizes a check on every one of its rooms, as room reads are
// authorized one room at a time, then reads them all on one session.
func (d *Dispatcher) readCheck(ctx context.Context, who actor, r core.Request, now time.Time) (core.CheckResult, error) {
	if !d.enabled(who, core.Check) {
		return core.CheckResult{}, ErrUnsupported
	}
	access, err := d.permissions(ctx, who)
	if err != nil {
		return core.CheckResult{}, err
	}
	if !slices.Contains(access.Operations, core.Check) {
		return core.CheckResult{}, auth.ErrForbidden
	}
	for _, room := range r.Rooms {
		if !access.allows(core.Target{Kind: "room", Name: room.Name}) {
			return core.CheckResult{}, auth.ErrForbidden
		}
	}
	// Only AIRC's own record proves who received a direct message, and it
	// records only accounts: a guest has no inbox here. A listener with a
	// target allowlist must list the author's own nickname.
	if r.Inbox != nil {
		author := who.author.Author
		own := core.Target{Kind: "direct", Name: author.Nick}
		if !author.Registered || own.Normalize() != nil || !access.allows(own) {
			return core.CheckResult{}, auth.ErrForbidden
		}
	}
	grant, err := d.authorizeRead(ctx, who, r, now)
	if err != nil {
		return core.CheckResult{}, err
	}
	mentionable := func(room string) bool { return access.allows(core.Target{Kind: "room", Name: room}) }
	return d.backend.Check(ctx, grant, mentionable)
}
