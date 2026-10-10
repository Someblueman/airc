//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func seedMigratingReplySignal(t *testing.T, db *sql.DB, realm, principal, binding string) []byte {
	t.Helper()
	r := replySignalStoreRequest(core.ReplySignalSet)
	r.RequestID = "migration-reply-signal"
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := core.UpstreamID(realm, binding, principal, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := core.ReplySignalAckBytes(replySignalStoreAck(core.ReplySignalSet), r, "author")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,reply_signal_outcome,terminal_at,send_attempts) VALUES (?,?,?,?,?,?,'retry','accepted',1000,1100,1160,604800,100,0,1,60,1000,1000,1,?,1000,1)`, principal, binding, r.RequestID, fingerprint, upstream, request, data)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func checkMigratedReplySignal(t *testing.T, s *Store, identity auth.Identity, expected []byte) {
	t.Helper()
	j, err := s.Lookup(context.Background(), identity, "migration-reply-signal")
	var retained []byte
	if queryErr := s.db.QueryRowContext(context.Background(), "SELECT reply_signal_outcome FROM jobs WHERE request_id='migration-reply-signal'").Scan(&retained); queryErr != nil || string(retained) != string(expected) || err != nil || !reflect.DeepEqual(j.Result.ReplySignal, replySignalStoreAck(core.ReplySignalSet)) || j.Result.State != core.Accepted || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Receipt != nil || j.Result.Poll != nil || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil {
		t.Fatalf("migration lost canonical reply-signal evidence: result=%+v error=%v", j.Result, err)
	}
}
