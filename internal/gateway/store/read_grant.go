package store

import (
	"context"
	"encoding/hex"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// ReadGrant is an internal one-use permission for one bounded room read or public-directory read.
// It creates no job or attempt budget. Raw upstream cursors stay inside this
// boundary; external ingress must first verify its scoped opaque cursor.
type ReadGrant struct {
	owner *Store
	used  bool
	read  ReadAttempt
}

const ReadLeaseDuration = 15 * time.Second

type ReadAttempt struct {
	Realm, Principal, Binding string
	Backend                   config.Binding
	Request                   core.Request
	Deadline                  time.Time
}

func (s *Store) AuthorizeRead(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (*ReadGrant, error) {
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if err := r.Normalize(); err != nil || r.Operation != core.HistoryRead && r.Operation != core.Search && r.Operation != core.Watch && r.Operation != core.Directory && r.Operation != core.ContextRead && r.Operation != core.PinsRead && r.Operation != core.RoomRead && r.Operation != core.WaitingRead && r.Operation != core.PollResults {
		return nil, ErrInvalid
	}
	if r.Operation != core.Directory && (r.Target == nil || r.Target.Kind != "room") {
		return nil, auth.ErrForbidden
	}
	if r.Cursor != "" && r.Cursor != "*" {
		if len(r.Cursor) != 32 {
			return nil, ErrInvalid
		}
		if _, err := hex.DecodeString(r.Cursor); err != nil {
			return nil, ErrInvalid
		}
	}
	if err := identity.Authorize(r); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	for _, p := range s.config.Principals {
		if p.ID != identity.ID() || p.Binding != identity.Binding() || p.Revoked {
			continue
		}
		if !slices.Contains(p.Operations, r.Operation) || r.Target != nil && !slices.Contains(p.Targets, *r.Target) {
			return nil, auth.ErrForbidden
		}
		var revoked bool
		if err := s.db.QueryRowContext(ctx, "SELECT revoked FROM authorization WHERE principal=? AND binding=?", p.ID, p.Binding).Scan(&revoked); err != nil {
			return nil, err
		}
		if revoked {
			return nil, auth.ErrForbidden
		}
		for _, b := range s.config.Bindings {
			if b.ID == p.Binding {
				return &ReadGrant{owner: s, read: ReadAttempt{Realm: s.config.Realm, Principal: p.ID, Binding: p.Binding, Backend: b, Request: r, Deadline: now.Add(ReadLeaseDuration)}}, nil
			}
		}
	}
	return nil, auth.ErrForbidden
}

// Consume orders against store close. As with delivery, an already authorized
// pre-revocation lease may finish until its fixed deadline, without reconnecting.
func (g *ReadGrant) Consume(now time.Time) (ReadAttempt, error) {
	if g == nil || g.owner == nil {
		return ReadAttempt{}, ErrNotReady
	}
	g.owner.mu.Lock()
	defer g.owner.mu.Unlock()
	if g.owner.closed {
		return ReadAttempt{}, ErrClosed
	}
	if g.used || !now.Before(g.read.Deadline) {
		return ReadAttempt{}, ErrNotReady
	}
	g.used = true
	a := g.read
	if a.Request.Target != nil {
		target := *a.Request.Target
		a.Request.Target = &target
	}
	return a, nil
}
