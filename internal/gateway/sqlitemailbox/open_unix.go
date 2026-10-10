//go:build darwin || linux

package sqlitemailbox

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strconv"

	"modernc.org/sqlite"
)

const (
	sqliteLimitLength         = 0
	sqliteLimitSQLLength      = 1
	sqliteLimitColumn         = 2
	sqliteLimitExpression     = 3
	sqliteLimitCompoundSelect = 4
	sqliteLimitFunctionArgs   = 6
	sqliteLimitAttached       = 7
	sqliteLimitVariables      = 9
)

func Open(ctx context.Context, directory string) (*Mailbox, error) {
	if ctx == nil {
		return nil, ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, dbFile, err := openMailboxFiles(directory)
	if err != nil {
		return nil, err
	}
	m := &Mailbox{directory: dir, database: dbFile, path: directory}
	fail := func(err error) (*Mailbox, error) {
		_ = m.Close()
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(directory, "mailbox.sqlite")}
	query := url.Values{}
	query.Set("mode", "rw")
	for _, pragma := range []string{"trusted_schema(OFF)", "synchronous(FULL)", "busy_timeout(250)", "mmap_size(0)", "cache_size(-64)"} {
		query.Add("_pragma", pragma)
	}
	u.RawQuery = query.Encode()
	m.pool, err = sql.Open("sqlite", u.String())
	if err != nil {
		return fail(ErrRequest)
	}
	m.pool.SetMaxOpenConns(1)
	m.pool.SetMaxIdleConns(1)
	m.conn, err = m.pool.Conn(ctx)
	if err != nil {
		return fail(ErrRequest)
	}
	if err := setSQLiteLimits(m.conn); err != nil {
		return fail(ErrRequest)
	}
	var pageSize, pageCount int64
	if err := m.conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil || pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		return fail(ErrRequest)
	}
	if err := m.conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil || pageCount <= 0 || pageCount*pageSize > MaxDatabaseBytes {
		return fail(ErrRequest)
	}
	maxPages := MaxDatabaseBytes / pageSize
	var actualPages int64
	if err := m.conn.QueryRowContext(ctx, "PRAGMA max_page_count="+strconv.FormatInt(maxPages, 10)).Scan(&actualPages); err != nil || actualPages < pageCount || actualPages > maxPages {
		return fail(ErrRequest)
	}
	if err := validateSchema(ctx, m.conn); err != nil {
		return fail(ErrRequest)
	}
	if err := verifyMailboxPaths(directory, dir, dbFile); err != nil {
		return fail(ErrRequest)
	}
	return m, nil
}

func setSQLiteLimits(conn *sql.Conn) error {
	for id, maximum := range map[int]int{
		sqliteLimitLength:         MaxRequestBytes + MaxResultBytes + 1024,
		sqliteLimitSQLLength:      4096,
		sqliteLimitColumn:         8,
		sqliteLimitExpression:     16,
		sqliteLimitCompoundSelect: 4,
		sqliteLimitFunctionArgs:   8,
		sqliteLimitAttached:       0,
		sqliteLimitVariables:      8,
	} {
		if _, err := sqlite.Limit(conn, id, maximum); err != nil {
			return err
		}
		actual, err := sqlite.Limit(conn, id, -1)
		if err != nil || actual > maximum {
			return errors.New("sqlite runtime limit unavailable")
		}
	}
	return nil
}
