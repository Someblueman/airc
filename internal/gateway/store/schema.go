package store

const schemaVersion = 2

const schema = `
CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;
CREATE TABLE jobs (
 principal TEXT NOT NULL, binding TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, upstream_id TEXT NOT NULL UNIQUE,
 request BLOB NOT NULL, policy TEXT NOT NULL CHECK(policy IN ('retry','receipt_only')),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','retry_wait','uncertain','accepted','rejected','expired','blocked')),
 admitted_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, replay_until INTEGER NOT NULL,
 retention_seconds INTEGER NOT NULL, send_limit INTEGER NOT NULL, lookup_limit INTEGER NOT NULL,
 backoff_seconds INTEGER NOT NULL, max_backoff_seconds INTEGER NOT NULL,
 send_attempts INTEGER NOT NULL DEFAULT 0, lookup_attempts INTEGER NOT NULL DEFAULT 0,
 next_send_at INTEGER NOT NULL, next_lookup_at INTEGER NOT NULL,
 attempt_id TEXT, attempt_deadline INTEGER, authorization_generation INTEGER,
 accepted INTEGER, persisted INTEGER, recipient_connected INTEGER,
 message_id TEXT, receipt BLOB, duplicate_risk INTEGER NOT NULL DEFAULT 0, last_error TEXT,
 terminal_at INTEGER,
 PRIMARY KEY(principal,binding,request_id)
) STRICT;
CREATE INDEX runnable_send ON jobs(state,next_send_at);
CREATE INDEX runnable_lookup ON jobs(state,next_lookup_at);
CREATE INDEX retention ON jobs(terminal_at);
CREATE TABLE replay (
 principal TEXT NOT NULL, binding TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, replay_until INTEGER NOT NULL,
 PRIMARY KEY(principal,binding,request_id)
) STRICT;
PRAGMA user_version=1;
`

const migration2 = `
CREATE TABLE authorization (
 principal TEXT NOT NULL, binding TEXT NOT NULL,
 revoked INTEGER NOT NULL CHECK(revoked IN (0,1)), generation INTEGER NOT NULL CHECK(generation>0),
 PRIMARY KEY(principal,binding)
) STRICT;
ALTER TABLE jobs ADD COLUMN attempt_kind TEXT CHECK(attempt_kind IN ('send','lookup'));
PRAGMA user_version=2;
`
