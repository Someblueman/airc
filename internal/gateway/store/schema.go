package store

import (
	"errors"
	"fmt"
)

// schemaVersion is the journal schema, the whole schema in one step with no
// migrations. Version 26 squashed versions 1–25; version 27 holds each
// accepted job's evidence in one outcome column; version 28 dropped the
// tables of the removed carriers (docs/gateway/removed-carriers.md).
// initialize creates it in an empty journal and refuses any older one
// (ErrPreviousJournal).
const schemaVersion = 28

// ErrPreviousJournal refuses a journal written by an earlier gateway, of any
// lower schema version. Its jobs cannot be carried over, so it is never
// opened or changed: drain it with the previous binary, then start this one
// with a new state directory.
var ErrPreviousJournal = errors.New("journal is from a previous gateway version; drain it with the previous binary, then use a new state directory")

func previousJournal(version int) error {
	return fmt.Errorf("journal schema %d: %w", version, ErrPreviousJournal)
}

// Jobs are keyed by (author_scope, request_id): listener is informational
// only, so one request ID resubmitted on any transport is one job. Account
// tokens are never stored; token_check is a digest used only to match a
// resubmission after restart. upstream_id is the request ID sent to AIRC,
// which scopes it per author, so two authors may share one. outcome holds
// an accepted job's evidence, encoded as its operation's core.Acceptance
// (a receipt or a native acknowledgement). The retention index on
// terminal_at serves Ready and Maintain, which read only unfinished jobs.
const schema = `
CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;
CREATE TABLE jobs (
 author_scope TEXT NOT NULL CHECK(length(author_scope) BETWEEN 3 AND 32 AND substr(author_scope,1,2) IN ('a:','g:')),
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 64),
 author_nick TEXT NOT NULL CHECK(length(author_nick) BETWEEN 1 AND 30 AND lower(author_nick)=substr(author_scope,3)),
 registered INTEGER NOT NULL CHECK(registered=(substr(author_scope,1,2)='a:')),
 listener TEXT NOT NULL DEFAULT '' CHECK(length(listener)<=64),
 token_check TEXT CHECK(token_check IS NULL OR (length(token_check)=64 AND token_check NOT GLOB '*[^0-9a-f]*')),
 account_id TEXT CHECK(account_id IS NULL OR length(account_id) BETWEEN 1 AND 64),
 resume_state TEXT CHECK(resume_state IS NULL OR resume_state IN ('queued','retry_wait','uncertain','rejected')),
 fingerprint TEXT NOT NULL, upstream_id TEXT NOT NULL,
 request BLOB NOT NULL, policy TEXT NOT NULL CHECK(policy IN ('retry','receipt_only')),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','retry_wait','uncertain','accepted','rejected','expired','blocked','credential_wait')),
 admitted_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, replay_until INTEGER NOT NULL,
 retention_seconds INTEGER NOT NULL, send_limit INTEGER NOT NULL, lookup_limit INTEGER NOT NULL,
 backoff_seconds INTEGER NOT NULL, max_backoff_seconds INTEGER NOT NULL,
 send_attempts INTEGER NOT NULL DEFAULT 0, lookup_attempts INTEGER NOT NULL DEFAULT 0,
 next_send_at INTEGER NOT NULL, next_lookup_at INTEGER NOT NULL,
 attempt_id TEXT, attempt_kind TEXT CHECK(attempt_kind IN ('send','lookup')), attempt_deadline INTEGER,
 accepted INTEGER, outcome BLOB,
 duplicate_risk INTEGER NOT NULL DEFAULT 0, last_error TEXT,
 terminal_at INTEGER,
 may_have_accepted INTEGER NOT NULL DEFAULT 0 CHECK(may_have_accepted IN (0,1)),
 failure_transient INTEGER NOT NULL DEFAULT 0 CHECK(failure_transient IN (0,1)),
 upstream_error INTEGER NOT NULL DEFAULT 0 CHECK(upstream_error BETWEEN 0 AND 999),
 send_blocked INTEGER NOT NULL DEFAULT 0 CHECK(send_blocked IN (0,1)),
 lookup_for_send INTEGER NOT NULL DEFAULT 0,
 CHECK(registered=(token_check IS NOT NULL)),
 CHECK((state='credential_wait')=(resume_state IS NOT NULL)),
 CHECK(state<>'credential_wait' OR registered=1),
 CHECK(outcome IS NULL OR state='accepted'),
 PRIMARY KEY(author_scope,request_id)
) STRICT;
CREATE INDEX runnable_send ON jobs(state,next_send_at);
CREATE INDEX runnable_lookup ON jobs(state,next_lookup_at);
CREATE INDEX retention ON jobs(terminal_at);
CREATE TABLE replay (
 author_scope TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, replay_until INTEGER NOT NULL,
 PRIMARY KEY(author_scope,request_id)
) STRICT;
PRAGMA user_version=28;
`
