//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestJournalMigrations(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} {
		t.Run(fmt.Sprint(version), func(t *testing.T) { testJournalMigration(t, version) })
	}
}

func testJournalMigration(t *testing.T, version int) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(c.StateDir, "journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	for _, migration := range journalMigrations[:version-1] {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO metadata VALUES('configuration',?)", pinnedConfiguration(c)); err != nil {
		t.Fatal(err)
	}
	r := request()
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	fingerprint, _ := r.Fingerprint()
	upstream, _ := legacyUpstreamID(c.Realm, "backend", "device", r.RequestID)
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES (?,?,?,?,?,?,'retry','queued',1000,1100,1160,604800,100,100,1,60,1000,1000)`, "device", "backend", r.RequestID, fingerprint, upstream, data); err != nil {
		t.Fatal(err)
	}
	if version == 2 {
		if _, err := db.ExecContext(ctx, "UPDATE jobs SET state='dispatching',attempt_id='old',attempt_kind='send',send_attempts=1"); err != nil {
			t.Fatal(err)
		}
	}
	if version >= 3 {
		nativeReceipt := receiptFor(Attempt{Job: Job{UpstreamID: upstream, Request: r}})
		receipt, err := core.ReceiptBytes(nativeReceipt, r, upstream, "author")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE jobs SET state='accepted',accepted=1,persisted=0,recipient_connected=0,message_id=?,receipt=?,terminal_at=1000,send_attempts=1 WHERE request_id=?", nativeReceipt.ID, receipt, r.RequestID); err != nil {
			t.Fatal(err)
		}
		if version >= 4 {
			cardReq := profileRequest()
			cardReq.RequestID = "migration-card"
			if err := cardReq.Normalize(); err != nil {
				t.Fatal(err)
			}
			cardRequest, _ := json.Marshal(cardReq)
			cardFingerprint, _ := cardReq.Fingerprint()
			cardUpstream, _ := legacyUpstreamID(c.Realm, "backend", "device", cardReq.RequestID)
			cardData, err := core.CardAckBytes(profileCard(), cardReq, "author")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,card_outcome,terminal_at) VALUES (?,?,?,?,?,?,'retry','accepted',1000,1100,1160,604800,100,0,1,60,1000,1000,1,?,1000)`, "device", "backend", cardReq.RequestID, cardFingerprint, cardUpstream, cardRequest, cardData); err != nil {
				t.Fatal(err)
			}
			if version >= 5 {
				topicReq := topicRequest()
				topicReq.RequestID = "migration-topic"
				if err := topicReq.Normalize(); err != nil {
					t.Fatal(err)
				}
				topicRequestData, _ := json.Marshal(topicReq)
				topicFingerprint, _ := topicReq.Fingerprint()
				topicUpstream, _ := legacyUpstreamID(c.Realm, "backend", "device", topicReq.RequestID)
				topicData, err := core.TopicAckBytes(topicAck(), topicReq, "author")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,topic_outcome,terminal_at) VALUES (?,?,?,?,?,?,'retry','accepted',1000,1100,1160,604800,100,0,1,60,1000,1000,1,?,1000)`, "device", "backend", topicReq.RequestID, topicFingerprint, topicUpstream, topicRequestData, topicData); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 6 {
				roomReq := roomRequest()
				roomReq.RequestID = "migration-room"
				if err := roomReq.Normalize(); err != nil {
					t.Fatal(err)
				}
				roomRequestData, _ := json.Marshal(roomReq)
				roomFingerprint, _ := roomReq.Fingerprint()
				roomUpstream, _ := legacyUpstreamID(c.Realm, "backend", "device", roomReq.RequestID)
				roomData, err := core.RoomAckBytes(roomAck(), roomReq)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,room_outcome,terminal_at) VALUES (?,?,?,?,?,?,'retry','accepted',1000,1100,1160,604800,100,0,1,60,1000,1000,1,?,1000)`, "device", "backend", roomReq.RequestID, roomFingerprint, roomUpstream, roomRequestData, roomData); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if version >= 7 {
		seedMigratingPin(t, db, c.Realm, "device", "backend")
	}
	if version >= 8 {
		seedMigratingActivity(t, db, c.Realm, "device", "backend")
	}
	if version >= 9 {
		seedMigratingReplySignal(t, db, c.Realm, "device", "backend")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// SQLite's fixture-created main file must satisfy production ownership policy.
	if err := os.Chmod(filepath.Join(c.StateDir, "journal.sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, c)
	if version < 3 {
		// The fixture's job is still queued (v1) or in flight (v2): its upstream
		// identity cannot be re-keyed, so the journal must be drained first.
		if !errors.Is(err, ErrUndrainedJournal) {
			t.Fatal("undrained journal migrated", err)
		}
		check, err := sql.Open("sqlite", filepath.Join(c.StateDir, "journal.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer check.Close()
		var actual, count int
		if err := check.QueryRowContext(ctx, "PRAGMA user_version").Scan(&actual); err != nil || actual != version {
			t.Fatal("refused journal was modified", actual, err)
		}
		if err := check.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE request_id=?", r.RequestID).Scan(&count); err != nil || count != 1 {
			t.Fatal("refused journal lost its job", count, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var actualVersion, count int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&actualVersion); err != nil || actualVersion != schemaVersion {
		t.Fatal("schema not migrated", err)
	}
	// The historical authorization table stays; nothing writes it any more.
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM authorization").Scan(&count); err != nil || count != 0 {
		t.Fatal("historical authorization table", count, err)
	}
	// Every older row was terminal; the author-keyed layout drops them all.
	for _, table := range []string{"jobs", "replay"} {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("terminal rows survived the re-key", table, count, err)
		}
	}
	for _, id := range []string{r.RequestID, "migration-card", "migration-topic", "migration-room", "migration-pin", "migration-activity", "migration-reply-signal"} {
		if _, err := s.LookupAuthor(ctx, identity, id); !errors.Is(err, ErrNotFound) {
			t.Fatal("dropped job still visible", id, err)
		}
	}
	checkAuthorKeyedJournal(t, s, c, identity)
}
