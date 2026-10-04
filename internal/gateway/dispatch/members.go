package dispatch

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ReadMembers is the bounded bearer members.read profile. The backend consumes
// one room-scoped read grant and owns the native session lifecycle.
func (d *Dispatcher) ReadMembers(ctx context.Context, token string, body []byte, now time.Time) (core.MembersResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.MembersResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.MembersRead {
		return core.MembersResult{}, store.ErrInvalid
	}
	return d.readMembers(ctx, identity, r, now)
}

func (d *Dispatcher) readMembers(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.MembersResult, error) {
	if !slices.Contains(d.operations, core.MembersRead) {
		return core.MembersResult{}, ErrUnsupported
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return core.MembersResult{}, err
	}
	if r.Target == nil || r.Target.Kind != "room" || !slices.Contains(access.Operations, core.MembersRead) || !slices.Contains(access.Targets, *r.Target) {
		return core.MembersResult{}, auth.ErrForbidden
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
	if err != nil {
		return core.MembersResult{}, err
	}
	result, err := d.backend.Members(ctx, grant)
	if err != nil {
		return core.MembersResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxMembersBytes || len(result.Members) > core.MaxMembers {
		return core.MembersResult{}, store.ErrCapacity
	}
	return result, nil
}
