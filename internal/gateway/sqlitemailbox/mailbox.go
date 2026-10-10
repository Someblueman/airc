// Package sqlitemailbox exposes one preprovisioned SQLite request/result slot.
package sqlitemailbox

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	MaxRequestBytes  = 16 << 10
	MaxResultBytes   = 64 << 10
	MaxDatabaseBytes = 8 << 20
	ApplicationID    = 0x4149524d // ASCII "AIRM".
	SchemaVersion    = 1
	SchemaSQL        = "CREATE TABLE mailbox (slot INTEGER PRIMARY KEY CHECK (slot = 1), request BLOB NOT NULL CHECK (length(request) BETWEEN 1 AND 16384), result BLOB CHECK (result IS NULL OR length(result) BETWEEN 1 AND 65536)) STRICT"
)

var (
	ErrRequest = errors.New("sqlite mailbox request failed")
	ErrClosed  = errors.New("sqlite mailbox is closed or unusable")
)

type Mailbox struct {
	mu        sync.Mutex
	conn      *sql.Conn
	pool      *sql.DB
	directory *os.File
	database  *os.File
	path      string
	failed    bool
	closed    bool
}

// Close joins Process, releases the ownership lock and closes SQLite handles.
func (m *Mailbox) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var closeErr error
	if m.conn != nil {
		closeErr = errors.Join(closeErr, m.conn.Close())
		m.conn = nil
	}
	if m.pool != nil {
		closeErr = errors.Join(closeErr, m.pool.Close())
		m.pool = nil
	}
	closeErr = errors.Join(closeErr, closeMailboxFiles(m.directory, m.database))
	m.directory, m.database = nil, nil
	return closeErr
}

// Process invokes handle only for an unread bounded request and stores its
// bounded output atomically in the same existing slot.
func (m *Mailbox) Process(ctx context.Context, handle func([]byte) ([]byte, error)) error {
	if m == nil || ctx == nil || handle == nil {
		return ErrRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.failed || m.conn == nil || ctx.Err() != nil {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrClosed
	}
	if err := verifyMailboxPaths(m.path, m.directory, m.database); err != nil {
		m.failed = true
		return ErrClosed
	}
	if _, err := m.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && (sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY || sqliteErr.Code()&0xff == sqlite3.SQLITE_LOCKED) {
			return ErrRequest
		}
		// Cancellation can race a successful BEGIN. Close this connection
		// rather than reuse an uncertain transaction on the next poll.
		m.failed = true
		return ErrClosed
	}
	txOpen := true
	defer func() {
		if txOpen {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := m.conn.ExecContext(rollbackCtx, "ROLLBACK"); err != nil {
				m.failed = true
			}
		}
	}()
	rollback := func(cause error) error {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := m.conn.ExecContext(rollbackCtx, "ROLLBACK"); err != nil {
			m.failed = true
			txOpen = false
			return errors.Join(ErrClosed, cause, err)
		}
		txOpen = false
		return cause
	}
	if err := validateSchema(ctx, m.conn); err != nil {
		return rollback(ErrRequest)
	}
	var request []byte
	var resultIsNull int
	err := m.conn.QueryRowContext(ctx, "SELECT request, result IS NULL FROM mailbox WHERE slot = 1").Scan(&request, &resultIsNull)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := m.conn.ExecContext(ctx, "COMMIT"); err != nil {
			return rollback(ErrRequest)
		}
		txOpen = false
		return m.verifyAfterCommit()
	}
	if err != nil || len(request) == 0 || len(request) > MaxRequestBytes || (resultIsNull != 0 && resultIsNull != 1) {
		clear(request)
		return rollback(ErrRequest)
	}
	if resultIsNull == 0 {
		clear(request)
		if _, err := m.conn.ExecContext(ctx, "COMMIT"); err != nil {
			return rollback(ErrRequest)
		}
		txOpen = false
		return m.verifyAfterCommit()
	}
	input := bytes.Clone(request)
	output, handleErr := handle(input)
	clear(input)
	if handleErr != nil {
		clear(output)
		clear(request)
		return rollback(handleErr)
	}
	if len(output) == 0 || len(output) > MaxResultBytes || ctx.Err() != nil {
		clear(output)
		clear(request)
		if ctx.Err() != nil {
			return rollback(ctx.Err())
		}
		return rollback(ErrRequest)
	}
	result, err := m.conn.ExecContext(ctx, "UPDATE mailbox SET result = ? WHERE slot = 1 AND result IS NULL AND request = ?", output, request)
	clear(output)
	clear(request)
	if err != nil {
		return rollback(ErrRequest)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return rollback(ErrRequest)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if _, err := m.conn.ExecContext(ctx, "COMMIT"); err != nil {
		return rollback(ErrRequest)
	}
	txOpen = false
	return m.verifyAfterCommit()
}

func (m *Mailbox) verifyAfterCommit() error {
	if err := verifyMailboxPaths(m.path, m.directory, m.database); err != nil {
		m.failed = true
		return ErrClosed
	}
	return nil
}

func validateSchema(ctx context.Context, conn *sql.Conn) error {
	var applicationID, version int
	if err := conn.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil || applicationID != ApplicationID {
		return fmt.Errorf("invalid mailbox application id")
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		return fmt.Errorf("invalid mailbox schema version")
	}
	var journalMode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil || journalMode != "delete" {
		return fmt.Errorf("invalid mailbox journal mode")
	}
	var synchronous, trusted, busy int
	if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		return fmt.Errorf("invalid mailbox synchronous mode")
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA trusted_schema").Scan(&trusted); err != nil || trusted != 0 {
		return fmt.Errorf("invalid mailbox trusted schema mode")
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil || busy < 0 || busy > 250 {
		return fmt.Errorf("invalid mailbox busy timeout")
	}
	var count int
	var schema string
	if err := conn.QueryRowContext(ctx, "SELECT count(*), min(sql) FROM sqlite_schema").Scan(&count, &schema); err != nil || count != 1 || schema != SchemaSQL {
		return fmt.Errorf("invalid mailbox schema")
	}
	var pageSize, pageCount, maxPages int64
	if err := conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil || pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		return fmt.Errorf("invalid mailbox page size")
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil || pageCount <= 0 || pageCount*pageSize > MaxDatabaseBytes {
		return fmt.Errorf("mailbox database exceeds size bound")
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA max_page_count").Scan(&maxPages); err != nil || maxPages < pageCount || maxPages*pageSize > MaxDatabaseBytes {
		return fmt.Errorf("invalid mailbox page limit")
	}
	return nil
}
