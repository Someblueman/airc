package store

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/Someblueman/airc/internal/gateway/couchdbwire"
)

const maxCouchDBProgressOwners = 128

// CouchDBProgress stores only the last fully processed changes sequence.
type CouchDBProgress struct {
	Revision int64
	Sequence string
}

func validCouchDBScope(scope string) bool {
	if len(scope) != 64 {
		return false
	}
	for _, c := range scope {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validCouchDBProgress(progress CouchDBProgress) bool {
	return progress.Revision >= 1 && couchdbwire.ValidSequence(progress.Sequence)
}

// ProvisionCouchDBProgress permanently binds a namespace to its config scope.
// Reopening an existing namespace with the same scope preserves its sequence.
func (s *Store) ProvisionCouchDBProgress(ctx context.Context, namespace, scope, initial string) (CouchDBProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return CouchDBProgress{}, ErrClosed
	}
	if ctx == nil || !couchdbwire.ValidNamespace(namespace) || !validCouchDBScope(scope) || !couchdbwire.ValidSequence(initial) {
		return CouchDBProgress{}, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return CouchDBProgress{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CouchDBProgress{}, err
	}
	defer tx.Rollback()
	current, currentScope, exists, err := readCouchDBProgress(ctx, tx, namespace)
	if err != nil {
		return CouchDBProgress{}, err
	}
	if exists {
		if currentScope != scope {
			return CouchDBProgress{}, ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return CouchDBProgress{}, err
		}
		return current, nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM couchdb_progress").Scan(&count); err != nil {
		return CouchDBProgress{}, err
	}
	if count >= maxCouchDBProgressOwners {
		return CouchDBProgress{}, ErrCapacity
	}
	progress := CouchDBProgress{Revision: 1, Sequence: initial}
	if _, err := tx.ExecContext(ctx, "INSERT INTO couchdb_progress(namespace,scope,revision,sequence) VALUES(?,?,?,?)", namespace, scope, progress.Revision, progress.Sequence); err != nil {
		return CouchDBProgress{}, err
	}
	if err := tx.Commit(); err != nil {
		return CouchDBProgress{}, err
	}
	return progress, nil
}

// AdvanceCouchDBProgress CASes the complete last processed checkpoint. Callers
// advance only after native admission of all changes through nextSequence.
func (s *Store) AdvanceCouchDBProgress(ctx context.Context, namespace, scope string, previous CouchDBProgress, nextSequence string) (CouchDBProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return CouchDBProgress{}, ErrClosed
	}
	if ctx == nil || !couchdbwire.ValidNamespace(namespace) || !validCouchDBScope(scope) || !validCouchDBProgress(previous) || !couchdbwire.ValidSequence(nextSequence) || previous.Revision == math.MaxInt64 {
		return CouchDBProgress{}, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return CouchDBProgress{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CouchDBProgress{}, err
	}
	defer tx.Rollback()
	current, currentScope, exists, err := readCouchDBProgress(ctx, tx, namespace)
	if err != nil {
		return CouchDBProgress{}, err
	}
	if !exists || currentScope != scope || current != previous {
		return CouchDBProgress{}, ErrConflict
	}
	next := CouchDBProgress{Revision: previous.Revision + 1, Sequence: nextSequence}
	result, err := tx.ExecContext(ctx, `UPDATE couchdb_progress SET revision=?, sequence=?
WHERE namespace=? AND scope=? AND revision=? AND sequence=?`, next.Revision, next.Sequence, namespace, scope, previous.Revision, previous.Sequence)
	if err != nil {
		return CouchDBProgress{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return CouchDBProgress{}, err
	}
	if changed != 1 {
		return CouchDBProgress{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return CouchDBProgress{}, err
	}
	return next, nil
}

func readCouchDBProgress(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, namespace string) (CouchDBProgress, string, bool, error) {
	var progress CouchDBProgress
	var scope string
	err := q.QueryRowContext(ctx, "SELECT scope,revision,sequence FROM couchdb_progress WHERE namespace=?", namespace).
		Scan(&scope, &progress.Revision, &progress.Sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return CouchDBProgress{}, "", false, nil
	}
	if err != nil {
		return CouchDBProgress{}, "", false, err
	}
	if !validCouchDBScope(scope) || !validCouchDBProgress(progress) {
		return CouchDBProgress{}, "", false, ErrInvalid
	}
	return progress, scope, true, nil
}
