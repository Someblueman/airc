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

// ReadLeaseDuration bounds every native read grant, including a whole watch.
// linkedReadLease is empty in release builds; see core.LinkedDuration.
var (
	ReadLeaseDuration = core.LinkedDuration(linkedReadLease, 15*time.Second)
	linkedReadLease   string
)

type ReadAttempt struct {
	Realm string
	// Author is who the read session logs in as; Scope owns its cursors and
	// limits. Credential is an author's in-memory token (empty for guests).
	Author     core.Author
	Scope      string
	Credential core.Credential
	// Source is the client source AIRC attributes an author read to.
	Source   string
	Backend  config.Upstream
	Request  core.Request
	Deadline time.Time
}

// Lane is the session-limit owner, matching delivery's per-lane fairness.
func (a ReadAttempt) Lane() string { return a.Scope }

// readable is the shared read boundary: one bounded native read of a room, or
// a global discovery read. Direct-message reads are never granted.
func readable(r *core.Request) error {
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	r.Rooms, r.Threads = slices.Clone(r.Rooms), slices.Clone(r.Threads)
	if r.Inbox != nil {
		inbox := *r.Inbox
		r.Inbox = &inbox
	}
	if err := r.Normalize(); err != nil || !r.Operation.IsNativeRead() {
		return ErrInvalid
	}
	// A check names only rooms, in Rooms.
	if r.Operation != core.Directory && r.Operation != core.ChannelsRead && r.Operation != core.AgentsRead && r.Operation != core.Check && (r.Target == nil || r.Target.Kind != "room") {
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

// Consume orders against store close. As with delivery, an already granted
// lease may finish until its fixed deadline, without reconnecting.
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
