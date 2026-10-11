//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func activityFixture(t *testing.T) (config.Config, auth.Identity) {
	t.Helper()
	c, _ := roomFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.ActivitySet)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	i, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	return c, i
}
func activityRequest() core.Request {
	return core.Request{Version: 1, RequestID: "activity-request", Operation: core.ActivitySet, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Activity: &core.ActivityInput{Kind: "typing", Note: "working", TTLSeconds: 5}}
}
func activityAck() *core.ActivityAcknowledgement {
	r := activityRequest()
	return &core.ActivityAcknowledgement{Version: 1, Operation: core.ActivitySet, Target: *r.Target, Kind: "thinking", From: "author", AccountID: "0123456789abcdef0123456789abcdef", Note: "native", ExpiresAt: time.Unix(1010, 0).UTC()}
}

func TestActivityAcceptancePersistsAndFencesRestart(t *testing.T) {
	c, identity := activityFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Admit(ctx, identity, activityRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	var limit int
	if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs WHERE request_id='activity-request'").Scan(&limit); err != nil || limit != 0 {
		t.Fatalf("activity lookup budget %d: %v", limit, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "activity-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	ack := activityAck()
	raw, err := core.ActivityAckBytes(ack, activityRequest(), "author")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordActivityAcceptance(ctx, g, ack, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "activity-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.Lookup(ctx, identity, "activity-request")
	var retained []byte
	if queryErr := s.db.QueryRowContext(ctx, "SELECT activity_outcome FROM jobs WHERE request_id='activity-request'").Scan(&retained); queryErr != nil || string(retained) != string(raw) || err != nil || j.Result.Activity == nil || *j.Result.Activity != *ack || j.Result.State != core.Accepted || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Result.Pin != nil || j.Result.Room != nil || j.Result.Topic != nil || j.Result.Card != nil || j.Receipt != nil || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil {
		t.Fatalf("activity facts changed: %#v %v", j, err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "activity-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("accepted activity resent after restart: %v", err)
	}
	if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Lookup(ctx, identity, "activity-request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("activity evidence not pruned at retention: %v", err)
	}
}

func TestActivityAcceptanceRetryKeepsNativeFacts(t *testing.T) {
	c, identity := activityFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.Admit(ctx, identity, activityRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "activity-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_activity BEFORE UPDATE OF activity_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	ack := activityAck()
	if _, err = s.RecordActivityAcceptance(ctx, g, ack, now, now); err == nil {
		t.Fatal("failed activity save claimed acceptance")
	}
	if _, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("completion erased unsaved activity evidence: %v", err)
	}
	ack.Note = "caller mutation"
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_activity"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Result.Activity == nil || j.Result.Activity.Note != "native" {
		t.Fatalf("activity retry changed facts: %#v %v", j, err)
	}
}

func TestActivityAcceptanceRejectsTargetAuthorAndDeadline(t *testing.T) {
	c, identity := activityFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.Admit(ctx, identity, activityRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "activity-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	wrong := activityAck()
	wrong.Target.Name = "#Other"
	if _, err = s.RecordActivityAcceptance(ctx, g, wrong, now, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted wrong target: %v", err)
	}
	wrong = activityAck()
	wrong.From = "other"
	if _, err = s.RecordActivityAcceptance(ctx, g, wrong, now, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted wrong author: %v", err)
	}
	if _, err = s.RecordActivityAcceptance(ctx, g, activityAck(), now.Add(31*time.Second), now.Add(31*time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted post-deadline activity: %v", err)
	}
}
