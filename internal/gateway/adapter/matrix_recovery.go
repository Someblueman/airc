package adapter

import (
	"context"
	"math"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// AdvanceMatrix recovers at most one bounded history page or cursor transition.
// Every sync anchor is reached through forward history in every provisioned
// room. Cold start begins at the first visible event, and limited sync pages
// cannot bypass history. The caller serially owns this recovery identity.
// This prerequisite does not run a service or publish responses.
func AdvanceMatrix(ctx context.Context, server *auth.MatrixServer, d *dispatch.Dispatcher, journal *store.Store, requests chan struct{}) (bool, error) {
	if ctx == nil || server == nil || d == nil || journal == nil || cap(requests) < 1 {
		return false, store.ErrInvalid
	}
	owner := server.RecoveryIdentity()
	rooms := server.Rooms()
	if owner == "" || len(rooms) == 0 {
		return false, store.ErrInvalid
	}
	position, err := journal.MatrixProgress(ctx, owner)
	if err != nil {
		return false, err
	}
	next := position
	if position.Next == "" {
		var timeline auth.VerifiedMatrixTimeline
		err := matrixRequest(ctx, requests, func(request context.Context) error {
			var err error
			timeline, err = server.ReadTimeline(request, position.Cursor)
			return err
		})
		if err != nil {
			return false, err
		}
		if !timeline.Valid() || timeline.Since() != position.Cursor {
			return false, auth.ErrAuthentication
		}
		if timeline.Cursor() == position.Cursor {
			return false, nil
		}
		// The new anchor is pending, never the committed position. Even a
		// gap-free initial sync is not permission to discard older history.
		next.Next = timeline.Cursor()
		next.RoomIndex = 0
		next.HistoryCursor = position.Cursor
	} else if position.RoomIndex == len(rooms) {
		// All room intervals have durable intake before the sync cursor moves.
		next.Cursor = position.Next
		next.Next, next.HistoryCursor = "", ""
		next.RoomIndex = 0
	} else {
		if position.RoomIndex < 0 || position.RoomIndex > len(rooms) {
			return false, store.ErrConflict
		}
		var page auth.VerifiedMatrixHistory
		err := matrixRequest(ctx, requests, func(request context.Context) error {
			var err error
			page, err = server.ReadHistory(request, rooms[position.RoomIndex], position.HistoryCursor, position.Next, "f")
			return err
		})
		if err != nil {
			return false, err
		}
		if !page.Valid() || page.Room() != rooms[position.RoomIndex] || page.From() != position.HistoryCursor || page.To() != position.Next || page.Direction() != "f" {
			return false, auth.ErrAuthentication
		}
		for _, event := range page.Events() {
			var rejected bool
			err := matrixRequest(ctx, requests, func(request context.Context) error {
				proof, reason, err := server.ReadMessage(request, event.Room, event.ID)
				if err != nil {
					return err
				}
				if reason != "" {
					if proof.Valid() || reason != "unmapped" && reason != "unsupported" && reason != "expired" && reason != "future" {
						return auth.ErrAuthentication
					}
					rejected = true
					return nil
				}
				result, err := d.Matrix(request, proof, time.Now())
				if err == nil && !result.Durable {
					return store.ErrInvalid
				}
				return err
			})
			if err != nil {
				// Earlier admissions may already be durable. Retry this same
				// page; the existing Matrix journal coalesces identical events.
				return false, err
			}
			if rejected {
				if next.Rejected == math.MaxInt64 {
					return false, store.ErrCapacity
				}
				next.Rejected++
			}
		}
		if page.Exhausted() || page.End() == position.Next {
			next.RoomIndex++
			next.HistoryCursor = position.Cursor
		} else {
			if page.End() == "" || page.End() == position.HistoryCursor {
				return false, store.ErrConflict
			}
			next.HistoryCursor = page.End()
		}
	}
	_, err = journal.SaveMatrixProgress(ctx, owner, position, next)
	return err == nil, err
}

// Each source request/admission releases shared capacity before the next event.
// Large pages cannot monopolize a slot for their entire history traversal.
func matrixRequest(parent context.Context, requests chan struct{}, run func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return run(ctx)
}
