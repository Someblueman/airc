package store

import (
	"errors"
	"fmt"
)

// schemaVersion is the journal schema. Version 26 is the whole schema in one
// step: the gateway was never deployed, so the migrations of versions 1–25
// were squashed into it. initialize creates it in an empty journal and
// refuses any older one (ErrPreviousJournal).
const schemaVersion = 26

// ErrPreviousJournal refuses a journal written by a gateway before the
// schema was squashed. Its jobs cannot be carried over, so it is never
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
// which scopes it per author, so two authors may share one. Exactly one
// outcome column holds an accepted job's result. The retention index on
// terminal_at serves Ready and Maintain, which read only unfinished jobs.
//
// Provider replay tables record the author scope (OSCORE: the listener) that
// first used an origin, engine or context.
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
 accepted INTEGER, persisted INTEGER, recipient_connected INTEGER,
 message_id TEXT, receipt BLOB,
 duplicate_risk INTEGER NOT NULL DEFAULT 0, last_error TEXT,
 terminal_at INTEGER,
 may_have_accepted INTEGER NOT NULL DEFAULT 0 CHECK(may_have_accepted IN (0,1)),
 failure_transient INTEGER NOT NULL DEFAULT 0 CHECK(failure_transient IN (0,1)),
 upstream_error INTEGER NOT NULL DEFAULT 0 CHECK(upstream_error BETWEEN 0 AND 999),
 send_blocked INTEGER NOT NULL DEFAULT 0 CHECK(send_blocked IN (0,1)),
 lookup_for_send INTEGER NOT NULL DEFAULT 0,
 card_outcome BLOB, topic_outcome BLOB, room_outcome BLOB, pin_outcome BLOB,
 activity_outcome BLOB, reply_signal_outcome BLOB, poll_outcome BLOB,
 CHECK(registered=(token_check IS NOT NULL)),
 CHECK((state='credential_wait')=(resume_state IS NOT NULL)),
 CHECK(state<>'credential_wait' OR registered=1),
 PRIMARY KEY(author_scope,request_id)
) STRICT;
CREATE INDEX runnable_send ON jobs(state,next_send_at);
CREATE INDEX runnable_lookup ON jobs(state,next_lookup_at);
CREATE INDEX retention ON jobs(terminal_at);
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
CREATE TABLE oscore_state (
 digest BLOB PRIMARY KEY CHECK(length(digest)=32),
 listener TEXT NOT NULL,
 sender_end INTEGER NOT NULL CHECK(sender_end>=0 AND sender_end<1099511627775),
 receiver_seq INTEGER CHECK(receiver_seq>=0 AND receiver_seq<1099511627775),
 receiver_window BLOB,
 CHECK((receiver_seq IS NULL AND receiver_window IS NULL) OR
       (receiver_seq IS NOT NULL AND receiver_window IS NOT NULL AND length(receiver_window)=8))
) STRICT;
CREATE TABLE posix_queue_ownership (
 listener TEXT PRIMARY KEY CHECK(length(listener) BETWEEN 1 AND 64),
 name TEXT NOT NULL UNIQUE CHECK(length(name) BETWEEN 2 AND 255),
 depth INTEGER NOT NULL CHECK(depth BETWEEN 1 AND 10),
 boot_id TEXT NOT NULL CHECK(length(boot_id)=36),
 namespace_device INTEGER NOT NULL CHECK(namespace_device>=0),
 namespace_inode INTEGER NOT NULL CHECK(namespace_inode>0),
 queue_device INTEGER NOT NULL CHECK(queue_device>=0),
 queue_inode INTEGER NOT NULL CHECK(queue_inode>0)
) STRICT;
CREATE TABLE system_v_queue_ownership (
 listener TEXT PRIMARY KEY CHECK(length(listener) BETWEEN 1 AND 64 AND listener NOT GLOB '*[^a-zA-Z0-9_-]*'),
 platform TEXT NOT NULL CHECK(platform IN ('darwin-arm64','linux-arm64','linux-amd64')),
 queue_key INTEGER NOT NULL UNIQUE CHECK(queue_key BETWEEN 1 AND 2147483647),
 queue_id INTEGER NOT NULL CHECK(queue_id BETWEEN 0 AND 2147483647),
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 0 AND 65535),
 changed INTEGER NOT NULL CHECK(changed>0),
 boot_id TEXT NOT NULL CHECK(length(boot_id)=36),
 namespace_device INTEGER NOT NULL CHECK(namespace_device>=0),
 namespace_inode INTEGER NOT NULL CHECK(namespace_inode>=0),
 CHECK((platform='darwin-arm64' AND namespace_device=0 AND namespace_inode=0) OR (platform IN ('linux-arm64','linux-amd64') AND namespace_inode>0))
) STRICT;
CREATE TABLE signed_syslog_state (
 signer BLOB NOT NULL CHECK(length(signer)=32),
 hostname TEXT NOT NULL CHECK(length(hostname) BETWEEN 1 AND 253),
 author_scope TEXT NOT NULL, target BLOB NOT NULL,
 reboot INTEGER NOT NULL CHECK(reboot BETWEEN 1 AND 9999999999),
 started TEXT NOT NULL,
 block INTEGER NOT NULL CHECK(block BETWEEN 0 AND 9999999999),
 last_message INTEGER NOT NULL CHECK(last_message BETWEEN 1 AND 9999999999),
 block_digest BLOB NOT NULL CHECK(length(block_digest)=32),
 PRIMARY KEY(signer,hostname)
) STRICT;
CREATE TABLE snmp_engines (
 engine BLOB PRIMARY KEY CHECK(length(engine) BETWEEN 5 AND 32),
 boots INTEGER NOT NULL CHECK(boots BETWEEN 0 AND 2147483646),
 engine_time INTEGER NOT NULL CHECK(engine_time BETWEEN 0 AND 2147483647),
 latest_received INTEGER NOT NULL CHECK(latest_received BETWEEN 0 AND 2147483647),
 anchor INTEGER NOT NULL CHECK(anchor>=0), last_seen INTEGER NOT NULL CHECK(last_seen>=anchor)
) STRICT;
CREATE TABLE snmp_owners (
 engine BLOB NOT NULL CHECK(length(engine) BETWEEN 5 AND 32),
 user TEXT NOT NULL CHECK(length(user) BETWEEN 1 AND 32),
 author_scope TEXT NOT NULL, target BLOB NOT NULL,
 context_engine BLOB NOT NULL CHECK(length(context_engine) BETWEEN 5 AND 32),
 context_name TEXT NOT NULL,
 PRIMARY KEY(engine,user)
) STRICT;
CREATE TABLE snmp_seen (
 request_id TEXT PRIMARY KEY CHECK(length(request_id)=64),
 engine BLOB NOT NULL CHECK(length(engine) BETWEEN 5 AND 32),
 user TEXT NOT NULL, boots INTEGER NOT NULL CHECK(boots BETWEEN 0 AND 2147483646),
 engine_time INTEGER NOT NULL CHECK(engine_time BETWEEN 0 AND 2147483647),
 digest BLOB NOT NULL CHECK(length(digest)=32)
) STRICT;
CREATE INDEX snmp_seen_clock ON snmp_seen(engine,boots,engine_time);
PRAGMA user_version=26;
`
