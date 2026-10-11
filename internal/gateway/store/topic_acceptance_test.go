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

func topicFixture(t *testing.T) (config.Config, auth.Identity) {
	t.Helper()
	c, _ := cardFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.TopicSet)
	validator, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := validator.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	return c, identity
}

func topicRequest() core.Request {
	topic := "hello"
	return core.Request{Version: 1, RequestID: "topic-request", Operation: core.TopicSet, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Topic: &topic}
}

func topicAck() *core.TopicAcknowledgement {
	return &core.TopicAcknowledgement{Version: 1, Operation: core.TopicSet, Topic: core.TopicResult{Target: core.Target{Kind: "room", Name: "#Allowed"}, Topic: "hello", SetBy: "author", SetAt: time.Unix(1000, 0).UTC()}}
}

func TestTopicAcceptancePersistsWithoutMessageFacts(t *testing.T) {
	c, identity := topicFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.Admit(ctx, identity, topicRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	var lookupLimit int
	if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs WHERE request_id='topic-request'").Scan(&lookupLimit); err != nil || lookupLimit != 0 {
		t.Fatalf("topic lookup budget = %d, %v", lookupLimit, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "topic-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordTopicAcceptance(ctx, g, topicAck(), now, now); err != nil {
		t.Fatal(err)
	}
	j, err := s.Lookup(ctx, identity, "topic-request")
	if err != nil || j.Result.Topic == nil || j.Receipt != nil || j.Result.Persisted != nil || j.Result.RecipientConnected != nil || j.Result.MessageID != "" {
		t.Fatalf("topic outcome malformed: %#v %v", j, err)
	}
}

func TestTopicAcceptanceRetryKeepsImmutableEvidence(t *testing.T) {
	c, identity := topicFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.Admit(ctx, identity, topicRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "topic-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_topic BEFORE UPDATE OF topic_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	ack := topicAck()
	if _, err = s.RecordTopicAcceptance(ctx, g, ack, now, now); err == nil {
		t.Fatal("failed topic save claimed acceptance")
	}
	ack.Topic.Topic = "caller mutation"
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_topic"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Result.Topic == nil || j.Result.Topic.Topic.Topic != "hello" {
		t.Fatalf("topic retry failed: %#v %v", j, err)
	}
}

func TestTopicSaveFailureRestartPolicies(t *testing.T) {
	for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
		t.Run(string(policy), func(t *testing.T) {
			c, identity := topicFixture(t)
			ctx := t.Context()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := topicRequest()
			// Omission exercises the default retry policy, as before.
			if policy == core.ReceiptOnly {
				r.DeliveryPolicy = &policy
			}
			if _, _, err = s.Admit(ctx, identity, r, 0, now); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = g.Consume(now); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_topic_restart BEFORE UPDATE OF topic_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.RecordTopicAcceptance(ctx, g, topicAck(), now, now); err == nil {
				t.Fatal("failed topic save claimed acceptance")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, LookupAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
				t.Fatalf("topic used lookup budget: %v", err)
			}
			if policy == core.Retry {
				j, err := s.Lookup(ctx, identity, r.RequestID)
				if err != nil || !j.Result.DuplicateRisk || !j.Result.AcceptanceUncertain || j.Result.NextAction != "send" {
					t.Fatalf("topic restart state: %#v %v", j, err)
				}
				if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now.Add(time.Second)); err != nil {
					t.Fatalf("topic retry not bounded: %v", err)
				}
			} else if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
				t.Fatalf("receipt-only topic resent: %v", err)
			}
		})
	}
}

func TestTopicAcceptanceRetentionAndPointerIsolation(t *testing.T) {
	c, identity := topicFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Admit(ctx, identity, topicRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "topic-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	*a.Job.Request.Topic = "caller mutation"
	if _, err = s.RecordTopicAcceptance(ctx, g, topicAck(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Lookup(ctx, identity, "topic-request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("topic was not pruned: %v", err)
	}
	s.Close()
}

func TestAllOutcomeColumnsAreExclusive(t *testing.T) {
	c, identity := topicFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.Admit(ctx, identity, topicRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	for _, columns := range []string{"receipt,card_outcome", "receipt,topic_outcome", "card_outcome,topic_outcome"} {
		statement := "UPDATE jobs SET "
		switch columns {
		case "receipt,card_outcome":
			statement += "receipt=?,card_outcome=?"
		case "receipt,topic_outcome":
			statement += "receipt=?,topic_outcome=?"
		case "card_outcome,topic_outcome":
			statement += "card_outcome=?,topic_outcome=?"
		}
		statement += " WHERE request_id='topic-request'"
		if _, err = s.db.ExecContext(ctx, statement, []byte(`{}`), []byte(`{}`)); err == nil {
			t.Fatalf("accepted simultaneous %s", columns)
		}
	}
}
