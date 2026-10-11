//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func seedMigratingActivity(t *testing.T, db *sql.DB, realm, principal, binding string) []byte {
	t.Helper()
	r := activityRequest()
	r.RequestID = "migration-activity"
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
	upstream, err := legacyUpstreamID(realm, binding, principal, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := core.ActivityAckBytes(activityAck(), r, "author")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,activity_outcome,terminal_at,send_attempts) VALUES (?,?,?,?,?,?,'retry','accepted',1000,1100,1160,604800,100,0,1,60,1000,1000,1,?,1000,1)`, principal, binding, r.RequestID, fingerprint, upstream, request, data)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
