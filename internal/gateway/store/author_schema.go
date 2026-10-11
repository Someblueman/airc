package store

import (
	"context"
	"database/sql"
	"errors"
)

// migration24 re-keys delivery and replay state by author scope. Job identity
// is (author_scope, request_id): listener is informational only, so one request
// ID resubmitted on any transport is one job. Account tokens are never stored;
// token_check is a digest used only to match a resubmission after restart.
// The principal/binding columns belonged to the removed identity model; new
// jobs leave them empty.
//
// The re-key cannot translate unfinished work (its upstream identity changes),
// so initialize refuses a journal with unfinished jobs before this runs; the
// remaining terminal rows and replay markers are dropped with the old tables.
const migration24 = `
DROP TABLE jobs;
DROP TABLE replay;
CREATE TABLE jobs (
 author_scope TEXT NOT NULL CHECK(length(author_scope) BETWEEN 3 AND 32 AND substr(author_scope,1,2) IN ('a:','g:')),
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 64),
 author_nick TEXT NOT NULL CHECK(length(author_nick) BETWEEN 1 AND 30 AND lower(author_nick)=substr(author_scope,3)),
 registered INTEGER NOT NULL CHECK(registered=(substr(author_scope,1,2)='a:')),
 listener TEXT NOT NULL DEFAULT '' CHECK(length(listener)<=64),
 token_check TEXT CHECK(token_check IS NULL OR (length(token_check)=64 AND token_check NOT GLOB '*[^0-9a-f]*')),
 account_id TEXT CHECK(account_id IS NULL OR length(account_id) BETWEEN 1 AND 64),
 resume_state TEXT CHECK(resume_state IS NULL OR resume_state IN ('queued','retry_wait','uncertain','rejected')),
 principal TEXT NOT NULL DEFAULT '', binding TEXT NOT NULL DEFAULT '',
 fingerprint TEXT NOT NULL, upstream_id TEXT NOT NULL UNIQUE,
 request BLOB NOT NULL, policy TEXT NOT NULL CHECK(policy IN ('retry','receipt_only')),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','retry_wait','uncertain','accepted','rejected','expired','blocked','credential_wait')),
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
 attempt_kind TEXT CHECK(attempt_kind IN ('send','lookup')),
 may_have_accepted INTEGER NOT NULL DEFAULT 0 CHECK(may_have_accepted IN (0,1)),
 failure_transient INTEGER NOT NULL DEFAULT 0 CHECK(failure_transient IN (0,1)),
 upstream_error INTEGER NOT NULL DEFAULT 0 CHECK(upstream_error BETWEEN 0 AND 999),
 send_blocked INTEGER NOT NULL DEFAULT 0 CHECK(send_blocked IN (0,1)),
 lookup_for_send INTEGER NOT NULL DEFAULT 0,
 card_outcome BLOB, topic_outcome BLOB, room_outcome BLOB, pin_outcome BLOB,
 activity_outcome BLOB, reply_signal_outcome BLOB, poll_outcome BLOB,
 CHECK((principal='')=(binding='')),
 CHECK(principal='' OR (registered=1 AND token_check IS NULL)),
 CHECK(registered=1 OR token_check IS NULL),
 CHECK(principal<>'' OR registered=0 OR token_check IS NOT NULL),
 CHECK((state='credential_wait')=(resume_state IS NOT NULL)),
 CHECK(state<>'credential_wait' OR (registered=1 AND principal='')),
 PRIMARY KEY(author_scope,request_id)
) STRICT;
CREATE INDEX runnable_send ON jobs(state,next_send_at);
CREATE INDEX runnable_lookup ON jobs(state,next_lookup_at);
CREATE INDEX retention ON jobs(terminal_at);
CREATE INDEX job_request ON jobs(request_id);
CREATE TRIGGER outcome_exclusive_insert BEFORE INSERT ON jobs
WHEN (NEW.receipt IS NOT NULL AND (NEW.card_outcome IS NOT NULL OR NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL OR NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.card_outcome IS NOT NULL AND (NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL OR NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.topic_outcome IS NOT NULL AND (NEW.room_outcome IS NOT NULL OR NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.room_outcome IS NOT NULL AND (NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.pin_outcome IS NOT NULL AND (NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.activity_outcome IS NOT NULL AND (NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.reply_signal_outcome IS NOT NULL AND NEW.poll_outcome IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'outcomes are mutually exclusive'); END;
CREATE TRIGGER outcome_exclusive_update BEFORE UPDATE OF receipt,card_outcome,topic_outcome,room_outcome,pin_outcome,activity_outcome,reply_signal_outcome,poll_outcome ON jobs
WHEN (NEW.receipt IS NOT NULL AND (NEW.card_outcome IS NOT NULL OR NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL OR NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.card_outcome IS NOT NULL AND (NEW.topic_outcome IS NOT NULL OR NEW.room_outcome IS NOT NULL OR NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.topic_outcome IS NOT NULL AND (NEW.room_outcome IS NOT NULL OR NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.room_outcome IS NOT NULL AND (NEW.pin_outcome IS NOT NULL OR NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.pin_outcome IS NOT NULL AND (NEW.activity_outcome IS NOT NULL OR NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.activity_outcome IS NOT NULL AND (NEW.reply_signal_outcome IS NOT NULL OR NEW.poll_outcome IS NOT NULL))
 OR (NEW.reply_signal_outcome IS NOT NULL AND NEW.poll_outcome IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'outcomes are mutually exclusive'); END;
CREATE TABLE replay (
 author_scope TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, replay_until INTEGER NOT NULL,
 PRIMARY KEY(author_scope,request_id)
) STRICT;
PRAGMA user_version=24;
`

// authorKeyVersion is the first journal version keyed by author scope.
const authorKeyVersion = 24

// ErrUndrainedJournal refuses an older journal that still owes delivery work.
var ErrUndrainedJournal = errors.New("journal has unfinished jobs from a previous gateway version; drain them with the previous binary before upgrading")

// refuseUndrained runs before any migration of a journal older than the
// author-keyed schema. Work in progress cannot be re-keyed: its upstream
// identity would change, risking a duplicate post or a lost receipt.
func refuseUndrained(ctx context.Context, tx *sql.Tx, version int) error {
	if version == 0 || version >= authorKeyVersion {
		return nil
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE terminal_at IS NULL OR attempt_id IS NOT NULL)`).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrUndrainedJournal
	}
	return nil
}

// ErrPinnedConfiguration refuses a journal created for another realm, or for
// a configuration of the removed identity model that named principals.
var ErrPinnedConfiguration = errors.New("journal belongs to another realm or to a configuration with removed principals; use a new state directory, or drain it with the previous binary")

// refuseLegacyJobs refuses a journal that still owes delivery work admitted
// under the removed identity model: its credential came from a removed
// configuration, so it can neither run nor wait for a resubmission.
func refuseLegacyJobs(ctx context.Context, tx *sql.Tx) error {
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE principal<>'' AND (terminal_at IS NULL OR attempt_id IS NOT NULL))`).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrUndrainedJournal
	}
	return nil
}
