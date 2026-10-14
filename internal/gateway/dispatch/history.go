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

func (d *Dispatcher) readPage(ctx context.Context, who actor, r core.Request, start string, now time.Time, emit func(backend.HistoryPage) error) (backend.HistoryPage, error) {
	var err error
	externalCursor := r.Cursor
	if r.Start != "" {
		if start != "" && start != r.Start {
			return backend.HistoryPage{}, store.ErrInvalid
		}
		start = r.Start
		r.Start = ""
	}
	if start != "" && !core.ValidStart(start) || r.Cursor != "" && start != "" || r.Operation == core.Search && start != "" {
		return backend.HistoryPage{}, store.ErrInvalid
	}
	if !d.enabled(who, r.Operation) {
		return backend.HistoryPage{}, ErrUnsupported
	}
	access, err := d.permissions(ctx, who)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	if !slices.Contains(access.Operations, r.Operation) || !access.allows(*r.Target) {
		return backend.HistoryPage{}, auth.ErrForbidden
	}
	if r.Target.Kind != "room" {
		return backend.HistoryPage{}, ErrUnsupported
	}
	author, err := d.authorScope(who)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	scope := auth.CursorScope{Realm: d.realm, Author: author, Operation: r.Operation, Target: *r.Target, Query: r.Query, From: r.From, ParentID: r.ParentID, ThreadMode: r.ThreadMode}
	if r.Cursor == "" {
		switch {
		case start == "retained" || start == "" && !r.Operation.IsWatch():
			r.Cursor = "*"
		case start != "recent" && start != "":
			// A message ID: AIRC starts after its position in history, or
			// at the oldest retained message with a gap once it has expired.
			r.Cursor = start
		}
	} else {
		r.Cursor, err = d.cursors.Open(scope, r.Cursor)
		if err != nil {
			return backend.HistoryPage{}, store.ErrInvalid
		}
	}
	var cursorCache *eventsCursorCache
	if r.Operation == core.EventsWatch {
		cursorCache = &eventsCursorCache{}
		if externalCursor != "" {
			cursorCache.raw, cursorCache.token = r.Cursor, externalCursor
		}
	}
	seal := d.sealPage
	if cursorCache != nil {
		seal = func(scope auth.CursorScope, page backend.HistoryPage) (backend.HistoryPage, error) {
			return d.sealEventsPage(scope, page, cursorCache)
		}
	}
	if emit != nil && !r.Operation.IsWatch() {
		return backend.HistoryPage{}, store.ErrInvalid
	}
	grant, err := d.authorizeRead(ctx, who, r, now)
	if err != nil {
		return backend.HistoryPage{}, err
	}
	var sink func(backend.HistoryPage) error
	if emit != nil {
		sink = func(page backend.HistoryPage) error {
			page, err := seal(scope, page)
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
	return seal(scope, page)
}

type eventsCursorCache struct {
	raw   string
	token string
}

func (d *Dispatcher) sealEventsPage(scope auth.CursorScope, page backend.HistoryPage, cache *eventsCursorCache) (backend.HistoryPage, error) {
	var err error
	if page.Cursor != "" {
		if cache.raw != page.Cursor || cache.token == "" {
			sealed, sealErr := d.cursors.Seal(scope, page.Cursor)
			if sealErr != nil {
				return backend.HistoryPage{}, store.ErrInvalid
			}
			cache.raw, cache.token = page.Cursor, sealed
		}
		page.Cursor = cache.token
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded)+1 > backend.MaxHistoryBytes {
		return backend.HistoryPage{}, store.ErrCapacity
	}
	return page, nil
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
