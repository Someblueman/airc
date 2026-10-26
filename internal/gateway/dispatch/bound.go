package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// BoundAuthor is an owner-configured default author for a local carrier
// (a mailbox, queue, shared memory, mapped file or an offline importer), so
// producers need not put an account token at rest in every request. The
// token stays in memory; a request that names its own author still runs as
// that author, exactly as on every other carrier.
type BoundAuthor struct {
	author     core.Author
	credential core.Credential
}

// NewBoundAuthor builds a bound author from a nickname and an optional
// account token (without one the bound author is a guest).
func NewBoundAuthor(nick, token string) (*BoundAuthor, error) {
	author, credential, err := core.NewAuthor(nick, token)
	if err != nil {
		return nil, err
	}
	return &BoundAuthor{author: author, credential: credential}, nil
}

// Author is the bound author (never its token).
func (b *BoundAuthor) Author() core.Author {
	if b == nil {
		return core.Author{}
	}
	return b.author
}

// Ingress returns a one-shot ingress for a local carrier: source names the
// client for AIRC ("" is the listener label), and a request without an
// author runs as the bound author, if any. Each call is a fresh session, so
// nothing one request does (such as account.identify) reaches another.
func (b *BoundAuthor) Ingress(source string) Ingress {
	s := NewSession()
	if b != nil {
		s.identify(b.author, b.credential)
	}
	return Ingress{Source: source, Session: s}
}

// ImportedJob is the outcome of admitting one imported mutation.
type ImportedJob struct {
	Author    core.Author
	RequestID string
	// Created is false when the journal already held this author's request
	// ID: a replay, which admits no new work.
	Created bool
	Result  core.Result
	owner   store.Submission
}

// ImportDecoded admits one decoded plain mutation for a local importer
// (mailbox files) under this listener's policy. Unlike Submit
// it reports whether the journal already held the request, so an importer
// can reject replays; the same request ID on any carrier is still one job.
func (d *Dispatcher) ImportDecoded(ctx context.Context, in Ingress, sub core.Submission, now time.Time) (ImportedJob, error) {
	author, credential, err := d.resolve(in, sub)
	if err != nil {
		return ImportedJob{}, err
	}
	who, r, err := d.admit(d.listener(), in.Source, author, credential, sub.Request, now)
	if err != nil {
		return ImportedJob{}, err
	}
	if !r.Operation.IsMutation() {
		return ImportedJob{}, ErrUnsupported
	}
	job, created, err := d.journal.AdmitAuthor(ctx, *who.author, r, 0, now)
	if err != nil {
		return ImportedJob{}, err
	}
	return ImportedJob{Author: author, RequestID: r.RequestID, Created: created, Result: job.Result, owner: *who.author}, nil
}

// Refresh reloads an imported job's current result from the journal.
func (d *Dispatcher) Refresh(ctx context.Context, job *ImportedJob) error {
	if d == nil || job == nil || job.RequestID == "" {
		return store.ErrInvalid
	}
	current, err := d.journal.LookupAuthor(ctx, job.owner, job.RequestID)
	if err != nil {
		return err
	}
	job.Result = current.Result
	return nil
}
