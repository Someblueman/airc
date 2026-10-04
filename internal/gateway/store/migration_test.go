//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestJournalMigrations(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
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
	if version >= 2 {
		if _, err := db.ExecContext(ctx, migration2); err != nil {
			t.Fatal(err)
		}
	}
	if version >= 3 {
		if _, err := db.ExecContext(ctx, migration3); err != nil {
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
	upstream, _ := core.UpstreamID(c.Realm, identity.Binding(), identity.ID(), r.RequestID)
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES (?,?,?,?,?,?,'retry','queued',1000,1100,1160,604800,100,100,1,60,1000,1000)`, identity.ID(), identity.Binding(), r.RequestID, fingerprint, upstream, data); err != nil {
		t.Fatal(err)
	}
	if version == 2 {
		if _, err := db.ExecContext(ctx, "UPDATE jobs SET state='dispatching',attempt_id='old',attempt_kind='send',send_attempts=1"); err != nil {
			t.Fatal(err)
		}
	}
	var originalReceipt []byte
	if version == 3 {
		nativeReceipt := receiptFor(Attempt{Job: Job{UpstreamID: upstream, Request: r}})
		receipt, err := core.ReceiptBytes(nativeReceipt, r, upstream, c.Bindings[0].Account)
		if err != nil {
			t.Fatal(err)
		}
		originalReceipt = receipt
		if _, err := db.ExecContext(ctx, "UPDATE jobs SET state='accepted',accepted=1,persisted=0,recipient_connected=0,message_id=?,receipt=?,terminal_at=1000,send_attempts=1 WHERE request_id=?", nativeReceipt.ID, receipt, r.RequestID); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// SQLite's fixture-created main file must satisfy production ownership policy.
	if err := os.Chmod(filepath.Join(c.StateDir, "journal.sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var actualVersion, count int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&actualVersion); err != nil || actualVersion != schemaVersion {
		t.Fatal("schema not migrated", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM authorization").Scan(&count); err != nil || count != 2 {
		t.Fatal("principals not migrated", err)
	}
	j, err := s.Lookup(ctx, identity, r.RequestID)
	if err != nil || j.UpstreamID != upstream || j.Result.ExpiresAt != 1100 || j.Request.Message != r.Message {
		t.Fatal("migration changed stored responsibility", j, err)
	}
	if version == 2 && (!j.Result.AcceptanceUncertain || j.Result.Accepted != nil || j.Result.SendAttempts != 1) {
		t.Fatal("migration lost old send uncertainty", j)
	}
	if version == 3 {
		var retained []byte
		if err := s.db.QueryRowContext(ctx, "SELECT receipt FROM jobs WHERE request_id=?", r.RequestID).Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if string(retained) != string(originalReceipt) {
			t.Fatalf("migration changed retained receipt: got %q want %q", retained, originalReceipt)
		}
		if j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.Card != nil || j.Receipt == nil || j.Result.MessageID != j.Receipt.ID || j.Result.Persisted == nil || *j.Result.Persisted || j.Result.RecipientConnected == nil || *j.Result.RecipientConnected {
			t.Fatal("migration lost accepted receipt", j)
		}
	}

}
