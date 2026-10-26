package store

import (
	"context"
	"database/sql"
	"errors"
)

// maxStatements bounds the prepared-statement cache. The journal's SQL is a
// fixed set well under it; anything past it runs unprepared.
const maxStatements = 128

// txn is a transaction on the store's one connection, begun and ended with
// explicit BEGIN and COMMIT rather than a database/sql Tx: inside a Tx on a
// Conn, database/sql prepares every statement afresh, and SQLite's parse
// and plan cost about a tenth of the gateway's CPU under load. A txn's
// statements run on the connection's prepared statements instead, which
// belong to the transaction because every caller holds s.mu, so nothing
// else runs on the connection until it ends.
type txn struct {
	s    *Store
	done bool
}

// begin starts a transaction that only its caller ends. Each statement takes
// ctx, which interrupts it, and commit refuses a cancelled ctx; the caller's
// deferred Rollback then ends the transaction before s.mu is free. Nothing
// ends it from another goroutine, so a cancelled caller cannot leave the
// next BEGIN inside a stale transaction.
func (s *Store) begin(ctx context.Context) (*txn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), "BEGIN"); err != nil {
		return nil, err
	}
	return &txn{s: s}, nil
}

// commit keeps database/sql's refusal to commit under a cancelled context.
// A failed COMMIT leaves SQLite's transaction open, so it is rolled back.
func commit(ctx context.Context, tx *txn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx.done {
		return sql.ErrTxDone
	}
	_, err := tx.s.db.ExecContext(context.WithoutCancel(ctx), "COMMIT")
	if err != nil {
		return errors.Join(err, tx.Rollback())
	}
	tx.done = true
	return nil
}

// Rollback ends the transaction unless it already ended, like Tx.Rollback.
func (tx *txn) Rollback() error {
	if tx.done {
		return sql.ErrTxDone
	}
	tx.done = true
	_, err := tx.s.db.ExecContext(context.Background(), "ROLLBACK")
	return err
}

func (tx *txn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if stmt := tx.s.statement(ctx, query); stmt != nil {
		return stmt.ExecContext(ctx, args...)
	}
	return tx.s.db.ExecContext(ctx, query, args...)
}

func (tx *txn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if stmt := tx.s.statement(ctx, query); stmt != nil {
		return stmt.QueryContext(ctx, args...)
	}
	return tx.s.db.QueryContext(ctx, query, args...)
}

func (tx *txn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if stmt := tx.s.statement(ctx, query); stmt != nil {
		return stmt.QueryRowContext(ctx, args...)
	}
	return tx.s.db.QueryRowContext(ctx, query, args...)
}

// statement returns query prepared on the store's connection, preparing it
// on first use, or nil to run it unprepared: past the cache's bound, or
// when preparing fails (the unprepared run then reports why). The caller
// holds s.mu.
func (s *Store) statement(ctx context.Context, query string) *sql.Stmt {
	if stmt := s.statements[query]; stmt != nil {
		return stmt
	}
	if len(s.statements) >= maxStatements {
		return nil
	}
	stmt, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil
	}
	if s.statements == nil {
		s.statements = map[string]*sql.Stmt{}
	}
	s.statements[query] = stmt
	return stmt
}
