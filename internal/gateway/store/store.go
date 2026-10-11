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
	"sync"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	_ "modernc.org/sqlite"
)

type Store struct {
	mu     sync.Mutex
	db     *sql.Conn
	pool   *sql.DB
	files  *files
	config config.Config
	closed bool
	// secrets holds registered authors' account tokens by job, in memory only.
	// A restart empties it; their unfinished jobs then wait in credential_wait.
	secrets map[string]core.Credential
	// sources holds each author job's client source (an address or a
	// listener label) in memory only, for aircd's per-source limits. After a
	// restart an attempt falls back to the job's listener label.
	sources map[string]string
	// admitted holds at most one pending signal that an admission added or
	// resumed a job; see Admitted.
	admitted chan struct{}
}

// Admitted receives after an admission adds a job or resumes one waiting
// for its credential, so the delivery runner can poll at once instead of on
// its next tick. It is a scheduling hint only: Ready and Reserve stay the
// gate, and signals coalesce. The signal is sent while the admission still
// holds the store lock, so a Ready it prompts runs after the commit (or
// finds nothing if the commit failed).
func (s *Store) Admitted() <-chan struct{} { return s.admitted }

func (s *Store) signalAdmitted() {
	select {
	case s.admitted <- struct{}{}:
	default:
	}
}

// Open requires an existing private local directory. It does not create parents,
// contact AIRC, or enable listeners. One connection serializes all transactions.
func Open(ctx context.Context, c config.Config) (*Store, error) {
	c.Listeners = slices.Clone(c.Listeners)
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
	s := &Store{files: f, config: c, secrets: map[string]core.Credential{}, sources: map[string]string{}, admitted: make(chan struct{}, 1)}
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
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&actual); err != nil {
		return fail(fmt.Errorf("journal page capacity unavailable: %w", err))
	}
	if actual != pages {
		return fail(fmt.Errorf("journal exceeds configured page capacity: requested %d pages, actual %d", pages, actual))
	}
	if err := s.initialize(ctx, version); err != nil {
		return fail(err)
	}
	if err := f.verify(); err != nil {
		return fail(err)
	}
	return s, nil
}

// pinnedConfiguration is the durable identity of a journal: its realm. The
// encoding keeps the two empty lists of the historical identity model, so
// journals created before it was removed still match.
func pinnedConfiguration(c config.Config) string {
	data, _ := json.Marshal(struct {
		Realm      string
		Bindings   []struct{}
		Principals []struct{}
	}{Realm: c.Realm})
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
			return ErrPinnedConfiguration
		}
	}
	if err := refuseUndrained(ctx, tx, version); err != nil {
		return err
	}
	for i, migration := range journalMigrations {
		if version < i+2 {
			if _, err := tx.ExecContext(ctx, migration); err != nil {
				return err
			}
		}
	}
	if err := refuseLegacyJobs(ctx, tx); err != nil {
		return err
	}
	// An old committed marker may have written. It cannot grant a new attempt
	// after restart; counters and deadlines survive, while the handle is cleared.
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state=CASE WHEN attempt_kind='send' AND state='dispatching' THEN CASE WHEN policy='retry' THEN 'retry_wait' ELSE 'uncertain' END ELSE state END, duplicate_risk=CASE WHEN attempt_kind='send' THEN 1 ELSE duplicate_risk END, may_have_accepted=CASE WHEN attempt_kind='send' THEN 1 ELSE may_have_accepted END, accepted=CASE WHEN attempt_kind='send' THEN NULL ELSE accepted END, attempt_id=NULL, attempt_kind=NULL WHERE attempt_id IS NOT NULL`); err != nil {
		return err
	}
	// Registered authors' tokens never reach disk, so this process has none:
	// their runnable work waits for a matching resubmission or its expiry.
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET resume_state=state, state='credential_wait' WHERE registered=1 AND terminal_at IS NULL AND state IN ('queued','retry_wait','uncertain','rejected')`); err != nil {
		return err
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
	clear(s.secrets)
	clear(s.sources)
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
