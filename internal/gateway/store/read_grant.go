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

// ReadGrant is an internal one-use permission for one bounded room read or global discovery read.
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
	// Author is who the read session logs in as; Scope owns its cursors and
	// limits. Credential is an author's in-memory token (empty for guests and
	// for the principal shim, whose token comes from the binding).
	Author     core.Author
	Scope      string
	Credential core.Credential
	// Source is the client source AIRC attributes an author read to.
	Source   string
	Backend  config.Binding
	Request  core.Request
	Deadline time.Time
}

// Lane is the session-limit owner, matching delivery's per-lane fairness.
func (a ReadAttempt) Lane() string { return laneOf(a.Principal, a.Scope) }

// readable is the shared read boundary: one bounded native read of a room, or
// a global discovery read. Direct-message reads are never granted.
func readable(r *core.Request) error {
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if err := r.Normalize(); err != nil || !r.Operation.IsNativeRead() {
		return ErrInvalid
	}
	if r.Operation != core.Directory && r.Operation != core.ChannelsRead && r.Operation != core.AgentsRead && (r.Target == nil || r.Target.Kind != "room") {
		return auth.ErrForbidden
	}
	if r.Cursor != "" && r.Cursor != "*" {
		if len(r.Cursor) != 32 {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(r.Cursor); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

// AuthorizeAuthorRead grants one read run as sub.Author, with AIRC's own
// semantics: a guest reads as an AIRC guest, a registered author after AIRC's
// login with its in-memory token. The listener's policy is the caller's.
func (s *Store) AuthorizeAuthorRead(ctx context.Context, sub Submission, r core.Request, now time.Time) (*ReadGrant, error) {
	if err := readable(&r); err != nil {
		return nil, err
	}
	owner, err := s.authorSubmitter(sub)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	upstream, ok := s.config.AuthorUpstream()
	if !ok {
		return nil, auth.ErrForbidden
	}
	source := owner.source
	if source == "" {
		source = ListenerSource(owner.listener)
	}
	return &ReadGrant{owner: s, read: ReadAttempt{Realm: s.config.Realm, Author: owner.author, Scope: owner.scope, Credential: owner.credential, Source: source, Backend: upstream, Request: r, Deadline: now.Add(ReadLeaseDuration)}}, nil
}

func (s *Store) AuthorizeRead(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (*ReadGrant, error) {
	if err := readable(&r); err != nil {
		return nil, err
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
		author, b, ok := s.config.PrincipalAuthor(p.ID)
		if ok && b.ID == p.Binding {
			return &ReadGrant{owner: s, read: ReadAttempt{Realm: s.config.Realm, Principal: p.ID, Binding: p.Binding, Author: author, Scope: author.Scope(), Backend: b, Request: r, Deadline: now.Add(ReadLeaseDuration)}}, nil
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
