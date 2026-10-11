//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func pinFixture(t *testing.T) (config.Config, Submission) {
	t.Helper()
	c, _ := roomFixture(t)
	i := fixtureAuthor()
	return c, i
}

func pinRequest() core.Request {
	return core.Request{Version: 1, RequestID: "pin-request", Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#Allowed"}, ParentID: "0123456789abcdef0123456789abcdef"}
}
func pinAck() *core.PinAcknowledgement {
	r := pinRequest()
	return &core.PinAcknowledgement{Version: 1, Operation: r.Operation, Target: *r.Target, ParentID: r.ParentID}
}

func TestPinAcceptancePersistsAndFencesResend(t *testing.T) {
	c, identity := pinFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if _, _, err = s.AdmitAuthor(ctx, identity, pinRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	var limit int
	if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs WHERE request_id='pin-request'").Scan(&limit); err != nil || limit != 0 {
		t.Fatalf("pin lookup budget %d: %v", limit, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "pin-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordPinAcceptance(ctx, g, pinAck(), now, now); err != nil {
		t.Fatal(err)
	}
	j, err := s.LookupAuthor(ctx, identity, "pin-request")
	if err != nil || j.Result.Pin == nil || *j.Result.Pin != *pinAck() || j.Result.Accepted == nil || !*j.Result.Accepted || j.Receipt != nil || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil || j.Result.Card != nil || j.Result.Topic != nil || j.Result.Room != nil {
		t.Fatalf("pin outcome malformed: %#v %v", j, err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "pin-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("accepted pin resent: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	j, err = s.LookupAuthor(ctx, identity, "pin-request")
	if err != nil || j.Result.Pin == nil || *j.Result.Pin != *pinAck() || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil {
		t.Fatalf("restart lost accepted pin evidence: %+v %v", j, err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "pin-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("restart resent accepted pin: %v", err)
	}
	if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupAuthor(ctx, identity, "pin-request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin outcome retention failed: %v", err)
	}
}

func TestPinAcceptanceRetryKeepsOriginalIntent(t *testing.T) {
	c, identity := pinFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, pinRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "pin-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_pin BEFORE UPDATE OF pin_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	ack := pinAck()
	if _, err = s.RecordPinAcceptance(ctx, g, ack, now, now); err == nil {
		t.Fatal("failed pin save claimed acceptance")
	}
	if _, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("failed pin save allowed completion to erase private evidence: %v", err)
	}
	ack.ParentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_pin"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Result.Pin == nil || j.Result.Pin.ParentID != pinRequest().ParentID {
		t.Fatalf("pin retry changed intent: %#v %v", j, err)
	}
}

func TestPinAcceptanceRejectsWrongIntentAndDeadline(t *testing.T) {
	c, identity := pinFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, pinRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "pin-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	wrong := pinAck()
	wrong.ParentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = s.RecordPinAcceptance(ctx, g, wrong, now, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted mismatched pin intent: %v", err)
	}
	if _, err = s.RecordPinAcceptance(ctx, g, pinAck(), now.Add(31*time.Second), now.Add(31*time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted post-deadline pin: %v", err)
	}
}

func TestPinOutcomePairwiseUpdateExclusivity(t *testing.T) {
	c, identity := pinFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, pinRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"receipt", "pin_outcome"}, {"card_outcome", "pin_outcome"}, {"topic_outcome", "pin_outcome"}, {"room_outcome", "pin_outcome"}} {
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+pair[0]+"=?, "+pair[1]+"=? WHERE request_id='pin-request'", []byte(`{}`), []byte(`{}`)); err == nil {
			t.Fatalf("accepted pair %v", pair)
		}
		insertID := "pin-insert-" + pair[0]
		upstream, err := core.UpstreamID(fixtureScope(c), insertID)
		if err != nil {
			t.Fatal(err)
		}
		query := "INSERT INTO jobs (author_scope,author_nick,registered,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at," + pair[0] + ",pin_outcome) SELECT author_scope,author_nick,registered,?,fingerprint,?,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,?,? FROM jobs WHERE request_id='pin-request'"
		if _, err = s.db.ExecContext(ctx, query, insertID, upstream, []byte(`{}`), []byte(`{}`)); err == nil {
			t.Fatalf("insert accepted pair %v", pair)
		}
		if _, err = s.db.ExecContext(ctx, query, insertID, upstream, []byte(`{}`), nil); err != nil {
			t.Fatalf("single-outcome insert positive control failed: %v", err)
		}
	}
}
