package store

const schemaVersion = 6

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
 message_id TEXT, receipt BLOB,
 duplicate_risk INTEGER NOT NULL DEFAULT 0, last_error TEXT,
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

const migration3 = `
ALTER TABLE jobs ADD COLUMN may_have_accepted INTEGER NOT NULL DEFAULT 0 CHECK(may_have_accepted IN (0,1));
ALTER TABLE jobs ADD COLUMN failure_transient INTEGER NOT NULL DEFAULT 0 CHECK(failure_transient IN (0,1));
ALTER TABLE jobs ADD COLUMN upstream_error INTEGER NOT NULL DEFAULT 0 CHECK(upstream_error BETWEEN 0 AND 999);
ALTER TABLE jobs ADD COLUMN send_blocked INTEGER NOT NULL DEFAULT 0 CHECK(send_blocked IN (0,1));
UPDATE jobs SET send_blocked=1 WHERE state IN ('rejected','blocked');
ALTER TABLE jobs ADD COLUMN lookup_for_send INTEGER NOT NULL DEFAULT 0;
UPDATE jobs SET lookup_for_send=send_attempts WHERE lookup_attempts>0 AND (attempt_kind='lookup' OR attempt_id IS NULL);
UPDATE jobs SET may_have_accepted=1 WHERE receipt IS NULL AND (state IN ('dispatching','retry_wait','uncertain') OR duplicate_risk=1);
PRAGMA user_version=3;
`

const migration4 = `
ALTER TABLE jobs ADD COLUMN card_outcome BLOB;
CREATE TRIGGER card_outcome_exclusive_insert BEFORE INSERT ON jobs
WHEN NEW.receipt IS NOT NULL AND NEW.card_outcome IS NOT NULL
BEGIN SELECT RAISE(ABORT,'receipt and card outcome are mutually exclusive'); END;
CREATE TRIGGER card_outcome_exclusive_update BEFORE UPDATE OF receipt,card_outcome ON jobs
WHEN NEW.receipt IS NOT NULL AND NEW.card_outcome IS NOT NULL
BEGIN SELECT RAISE(ABORT,'receipt and card outcome are mutually exclusive'); END;
PRAGMA user_version=4;
`

const migration5 = `
ALTER TABLE jobs ADD COLUMN topic_outcome BLOB;
DROP TRIGGER card_outcome_exclusive_insert;
DROP TRIGGER card_outcome_exclusive_update;
CREATE TRIGGER outcome_exclusive_insert BEFORE INSERT ON jobs
WHEN (NEW.receipt IS NOT NULL AND (NEW.card_outcome IS NOT NULL OR NEW.topic_outcome IS NOT NULL))
 OR (NEW.card_outcome IS NOT NULL AND NEW.topic_outcome IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'receipt, card, and topic outcomes are mutually exclusive'); END;
CREATE TRIGGER outcome_exclusive_update BEFORE UPDATE OF receipt,card_outcome,topic_outcome ON jobs
WHEN (NEW.receipt IS NOT NULL AND (NEW.card_outcome IS NOT NULL OR NEW.topic_outcome IS NOT NULL))
 OR (NEW.card_outcome IS NOT NULL AND NEW.topic_outcome IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'receipt, card, and topic outcomes are mutually exclusive'); END;
PRAGMA user_version=5;
`

const migration6 = `
ALTER TABLE jobs ADD COLUMN room_outcome BLOB;
DROP TRIGGER outcome_exclusive_insert;
DROP TRIGGER outcome_exclusive_update;
CREATE TRIGGER outcome_exclusive_insert BEFORE INSERT ON jobs
WHEN (NEW.receipt IS NOT NULL AND (NEW.card_outcome IS NOT NULL OR NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL))
 OR (NEW.card_outcome IS NOT NULL AND (NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL))
 OR (NEW.topic_outcome IS NOT NULL AND NEW.room_outcome IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'outcomes are mutually exclusive'); END;
CREATE TRIGGER outcome_exclusive_update BEFORE UPDATE OF receipt,card_outcome,topic_outcome,room_outcome ON jobs
WHEN (NEW.receipt IS NOT NULL AND (NEW.card_outcome IS NOT NULL OR NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL))
 OR (NEW.card_outcome IS NOT NULL AND (NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL))
 OR (NEW.topic_outcome IS NOT NULL AND NEW.room_outcome IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'outcomes are mutually exclusive'); END;
PRAGMA user_version=6;
`
