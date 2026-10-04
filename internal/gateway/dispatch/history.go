package dispatch

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ReadPage is the bounded bearer history/search profile. All returned cursors are opaque;
// an empty input starts retained history unless start is recent. No raw
// upstream ID is accepted; a start mode cannot change a resumed position.
func (d *Dispatcher) ReadPage(ctx context.Context, token string, body []byte, start string, now time.Time) (backend.HistoryPage, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.HistoryRead && r.Operation != core.Search {
		return backend.HistoryPage{}, store.ErrInvalid
	}
	if start != "" && start != "recent" && start != "retained" || r.Cursor != "" && start != "" || r.Operation == core.Search && start != "" {
		return backend.HistoryPage{}, store.ErrInvalid
	}
	if !slices.Contains(d.operations, r.Operation) {
		return backend.HistoryPage{}, ErrUnsupported
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	if !slices.Contains(access.Operations, r.Operation) || !slices.Contains(access.Targets, *r.Target) {
		return backend.HistoryPage{}, auth.ErrForbidden
	}
	if r.Target.Kind != "room" {
		return backend.HistoryPage{}, ErrUnsupported
	}
	scope := auth.CursorScope{Realm: d.realm, Principal: identity.ID(), Binding: identity.Binding(), Operation: r.Operation, Target: *r.Target, Query: r.Query, From: r.From}
	if r.Cursor == "" {
		if start != "recent" {
			r.Cursor = "*"
		}
	} else {
		r.Cursor, err = d.cursors.Open(scope, r.Cursor)
		if err != nil {
			return backend.HistoryPage{}, store.ErrInvalid
		}
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	page, err := d.backend.Read(ctx, grant)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	if page.Cursor != "" {
		page.Cursor, err = d.cursors.Seal(scope, page.Cursor)
		if err != nil {
			return backend.HistoryPage{}, store.ErrInvalid
		}
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded)+1 > backend.MaxHistoryBytes {
		return backend.HistoryPage{}, store.ErrCapacity
	}
	return page, nil
}
