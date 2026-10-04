// Package store owns the gateway's on-disk delivery responsibility. It has no
// network side effects; callers may acknowledge intake only after Admit succeeds.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/config"
	_ "modernc.org/sqlite"
)

type Store struct {
	mu     sync.Mutex
	db     *sql.Conn
	pool   *sql.DB
	files  *files
	config config.Config
	closed bool
}

// Open requires an existing private local directory. It does not create parents,
// contact AIRC, or enable listeners. One connection serializes all transactions.
func Open(ctx context.Context, c config.Config) (*Store, error) {
	c.Bindings = slices.Clone(c.Bindings)
	c.Principals = slices.Clone(c.Principals)
	c.Listeners = slices.Clone(c.Listeners)
	for i := range c.Principals {
		c.Principals[i].Targets = slices.Clone(c.Principals[i].Targets)
		c.Principals[i].Operations = slices.Clone(c.Principals[i].Operations)
		c.Principals[i].SigningKeys = slices.Clone(c.Principals[i].SigningKeys)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.CheckFiles(); err != nil {
		return nil, err
	}
	f, err := openFiles(c.StateDir)
	if err != nil {
		return nil, err
	}
	s := &Store{files: f, config: c}
	fail := func(err error) (*Store, error) { _ = s.Close(); return nil, err }
	u := url.URL{Scheme: "file", Path: filepath.Join(c.StateDir, "journal.sqlite")}
	query := url.Values{}
	for _, pragma := range []string{"synchronous(FULL)", "foreign_keys(ON)", "busy_timeout(1000)", "wal_autocheckpoint(1)"} {
		query.Add("_pragma", pragma)
	}
	u.RawQuery = query.Encode()
	s.pool, err = sql.Open("sqlite", u.String())
	if err != nil {
		return fail(err)
	}
	s.pool.SetMaxOpenConns(1)
	s.pool.SetMaxIdleConns(1)
	s.db, err = s.pool.Conn(ctx)
	if err != nil {
		return fail(err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > schemaVersion || version < 0 {
		return fail(errors.New("unsupported journal schema version"))
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		return fail(errors.New("journal WAL unavailable"))
	}
	for pragma, expected := range map[string]int{"synchronous": 2, "foreign_keys": 1, "wal_autocheckpoint": 1} {
		var actual int
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&actual); err != nil || actual != expected {
			return fail(fmt.Errorf("journal pragma %s unavailable", pragma))
		}
	}
	var pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil || pageSize < 512 {
		return fail(errors.New("invalid journal page size"))
	}
	// Reserve space for WAL and its index; a full database fails before admission.
	pages := (c.Limits.JournalBytes - 65536) / 3 / pageSize
	var actual int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&actual); err != nil || actual != pages {
		return fail(errors.New("journal exceeds configured page capacity"))
	}
	if err := s.initialize(ctx, version); err != nil {
		return fail(err)
	}
	if err := f.verify(); err != nil {
		return fail(err)
	}
	return s, nil
}

// pinnedConfiguration excludes credential files/material so credential rotation
// cannot change durable job identity. Endpoint/author rebinding is rejected.
func pinnedConfiguration(c config.Config) string {
	type binding struct{ ID, Network, Address, Account, TLSServerName string }
	type principal struct{ ID, Binding string }
	p := struct {
		Realm      string
		Bindings   []binding
		Principals []principal
	}{Realm: c.Realm}
	for _, b := range c.Bindings {
		p.Bindings = append(p.Bindings, binding{b.ID, b.Network, b.Address, b.Account, b.TLSServerName})
	}
	for _, i := range c.Principals {
		p.Principals = append(p.Principals, principal{i.ID, i.Binding})
	}
	slices.SortFunc(p.Bindings, func(a, b binding) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(p.Principals, func(a, b principal) int { return strings.Compare(a.ID, b.ID) })
	data, _ := json.Marshal(p)
	return string(data)
}

func (s *Store) initialize(ctx context.Context, version int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	pinned := pinnedConfiguration(s.config)
	if version == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unversioned nonempty journal refused")
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES ('configuration',?)", pinned); err != nil {
			return err
		}
	} else {
		var stored string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='configuration'").Scan(&stored); err != nil {
			return err
		}
		if stored != pinned {
			return errors.New("durable realm or identity binding changed")
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.pool != nil {
		err = errors.Join(err, s.pool.Close())
	}
	if s.files != nil {
		err = errors.Join(err, s.files.close())
	}
	return err
}
