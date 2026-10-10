//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func seedMigratingPin(t *testing.T, db *sql.DB, realm, principal, binding string) []byte {
	t.Helper()
	r := pinRequest()
	r.RequestID = "migration-pin"
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
	data, err := core.PinAckBytes(pinAck(), r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,pin_outcome,terminal_at,send_attempts) VALUES (?,?,?,?,?,?,'retry','accepted',1000,1100,1160,604800,100,0,1,60,1000,1000,1,?,1000,1)`, principal, binding, r.RequestID, fingerprint, upstream, request, data)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func checkMigratedPin(t *testing.T, s *Store, identity auth.Identity, expected []byte) {
	t.Helper()
	j, err := s.Lookup(context.Background(), identity, "migration-pin")
	var retained []byte
	if queryErr := s.db.QueryRowContext(context.Background(), "SELECT pin_outcome FROM jobs WHERE request_id='migration-pin'").Scan(&retained); queryErr != nil || string(retained) != string(expected) || err != nil || j.Result.Pin == nil || *j.Result.Pin != *pinAck() || j.Result.State != core.Accepted || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Receipt != nil || j.Result.Card != nil || j.Result.Topic != nil || j.Result.Room != nil || j.Result.Activity != nil || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil {
		t.Fatalf("migration lost canonical pin evidence: result=%+v error=%v", j.Result, err)
	}
}
