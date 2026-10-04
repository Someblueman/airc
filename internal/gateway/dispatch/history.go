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

// ReadPage is the bounded bearer history/search/watch profile. All returned cursors are opaque;
// An empty input starts retained history/search or recent watch history.
// No raw upstream ID is accepted; start cannot change a resumed position.
func (d *Dispatcher) ReadPage(ctx context.Context, token string, body []byte, start string, now time.Time, emit func(backend.HistoryPage) error) (backend.HistoryPage, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.HistoryRead && r.Operation != core.Search && r.Operation != core.Watch {
		return backend.HistoryPage{}, store.ErrInvalid
	}
	return d.readPage(ctx, identity, r, start, now, emit)
}

func (d *Dispatcher) readPage(ctx context.Context, identity auth.Identity, r core.Request, start string, now time.Time, emit func(backend.HistoryPage) error) (backend.HistoryPage, error) {
	var err error
	if r.Start != "" {
		if start != "" && start != r.Start {
			return backend.HistoryPage{}, store.ErrInvalid
		}
		start = r.Start
		r.Start = ""
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
	scope := auth.CursorScope{Realm: d.realm, Principal: identity.ID(), Binding: identity.Binding(), Operation: r.Operation, Target: *r.Target, Query: r.Query, From: r.From, ParentID: r.ParentID, ThreadMode: r.ThreadMode}
	if r.Cursor == "" {
		if start == "retained" || start == "" && r.Operation != core.Watch {
			r.Cursor = "*"
		}
	} else {
		r.Cursor, err = d.cursors.Open(scope, r.Cursor)
		if err != nil {
			return backend.HistoryPage{}, store.ErrInvalid
		}
	}
	if emit != nil && r.Operation != core.Watch {
		return backend.HistoryPage{}, store.ErrInvalid
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	var sink func(backend.HistoryPage) error
	if emit != nil {
		sink = func(page backend.HistoryPage) error {
			page, err := d.sealPage(scope, page)
			if err != nil {
				return err
			}
			return emit(page)
		}
	}
	page, err := d.backend.Read(ctx, grant, sink)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	return d.sealPage(scope, page)
}

func (d *Dispatcher) sealPage(scope auth.CursorScope, page backend.HistoryPage) (backend.HistoryPage, error) {
	var err error
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
