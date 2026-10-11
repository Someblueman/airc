//go:build darwin || linux

package store

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestActivityOutcomeInsertAndUpdateExclusivity(t *testing.T) {
	c, identity := activityFixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, activityRequest(), 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"receipt", "card_outcome", "topic_outcome", "room_outcome", "pin_outcome"} {
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+column+"=?,activity_outcome=? WHERE request_id='activity-request'", []byte(`{}`), []byte(`{}`)); err == nil {
			t.Fatalf("UPDATE allowed activity with %s", column)
		}
		id := "activity-insert-" + column
		upstream, err := core.UpstreamID(c.Realm, fixtureScope(c), id)
		if err != nil {
			t.Fatal(err)
		}
		query := "INSERT INTO jobs (author_scope,author_nick,registered,principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at," + column + ",activity_outcome) SELECT author_scope,author_nick,registered,principal,binding,?,fingerprint,?,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,?,? FROM jobs WHERE request_id='activity-request'"
		if _, err = s.db.ExecContext(ctx, query, id, upstream, []byte(`{}`), []byte(`{}`)); err == nil {
			t.Fatalf("INSERT allowed activity with %s", column)
		}
		if _, err = s.db.ExecContext(ctx, query, id, upstream, []byte(`{}`), nil); err != nil {
			t.Fatalf("single-outcome INSERT control %s: %v", column, err)
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+column+"=NULL,activity_outcome=? WHERE request_id=?", []byte(`{}`), id); err != nil {
			t.Fatalf("activity-only UPDATE control %s: %v", column, err)
		}
	}
}

func TestActivityGrantCopiesPrivateIntent(t *testing.T) {
	c, identity := activityFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := activityRequest()
	if _, _, err = s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	r.Activity.Kind = "thinking"
	r.Target.Name = "#CallerChanged"
	g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Job.Request.Activity.Kind != activityRequest().Activity.Kind || *a.Job.Request.Target != *activityRequest().Target {
		t.Fatal("caller changed admitted intent", a.Job.Request)
	}
	a.Job.Request.Activity.Note = "backend changed"
	a.Job.Request.Target.Name = "#BackendChanged"
	if _, err = s.RecordActivityAcceptance(ctx, g, activityAck(), now, now); err != nil {
		t.Fatalf("exposed attempt altered private acceptance intent: %v", err)
	}
}
