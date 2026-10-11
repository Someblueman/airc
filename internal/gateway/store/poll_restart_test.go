//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPollAcceptanceRestartAndPrivateOptionCopies(t *testing.T) {
	for _, op := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
		t.Run(string(op), func(t *testing.T) {
			c, identity := pollFixture(t)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			r := pollRequest(op)
			if _, _, err = s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
			if err != nil {
				t.Fatal(err)
			}
			a, err := g.Consume(now)
			if err != nil {
				t.Fatal(err)
			}
			if a.Job.Request.Poll != nil {
				a.Job.Request.Poll.Options[0] = "backend mutation"
				if g.state.attempt.Job.Request.Poll.Options[0] != "one" {
					t.Fatal("consumed options alias private intent")
				}
			}
			ack := pollAck(r, op)
			if op == core.PollCreate {
				ack.Receipt.Receipt.Persisted = false
			}
			want, err := core.PollAckBytes(ack, r, "author")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.RecordPollAcceptance(ctx, g, ack, now, now); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			j, err := s.LookupAuthor(ctx, identity, r.RequestID)
			if err != nil || !reflect.DeepEqual(j.Result.Poll, ack) || j.Receipt != nil || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 {
				t.Fatalf("restart lost compound evidence: %+v error=%v", j.Result, err)
			}
			var receipt, retained []byte
			if err = s.db.QueryRowContext(ctx, "SELECT receipt,poll_outcome FROM jobs WHERE request_id=?", r.RequestID).Scan(&receipt, &retained); err != nil || receipt != nil || string(retained) != string(want) {
				t.Fatalf("canonical compound evidence: %v", err)
			}
			if op == core.PollCreate && (j.Result.Persisted == nil || *j.Result.Persisted || j.Result.MessageID != ack.Poll.ID || j.Result.RecipientConnected != nil) {
				t.Fatal("false persistence fact changed", j.Result)
			}
			if op != core.PollCreate && (j.Result.Persisted != nil || j.Result.MessageID != "" || j.Result.RecipientConnected != nil) {
				t.Fatal("fabricated poll receipt", j.Result)
			}
			if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
				t.Fatalf("accepted poll resent: %v", err)
			}
		})
	}
}

func TestPollOutcomeInsertUpdateExclusivity(t *testing.T) {
	c, identity := pollFixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := pollRequest(core.PollVote)
	if _, _, err = s.AdmitAuthor(ctx, identity, r, 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"receipt", "card_outcome", "topic_outcome", "room_outcome", "pin_outcome", "activity_outcome", "reply_signal_outcome"} {
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+column+"=?,poll_outcome=? WHERE request_id=?", []byte(`{}`), []byte(`{}`), r.RequestID); err == nil {
			t.Fatalf("UPDATE allowed poll with %s", column)
		}
		id := "poll-insert-" + column
		upstream, err := core.UpstreamID(fixtureScope(c), id)
		if err != nil {
			t.Fatal(err)
		}
		query := "INSERT INTO jobs (author_scope,author_nick,registered,principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at," + column + ",poll_outcome) SELECT author_scope,author_nick,registered,principal,binding,?,fingerprint,?,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,?,? FROM jobs WHERE request_id=?"
		if _, err = s.db.ExecContext(ctx, query, id, upstream, []byte(`{}`), []byte(`{}`), r.RequestID); err == nil {
			t.Fatalf("INSERT allowed poll with %s", column)
		}
		if _, err = s.db.ExecContext(ctx, query, id, upstream, []byte(`{}`), nil, r.RequestID); err != nil {
			t.Fatalf("single prior outcome %s: %v", column, err)
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+column+"=NULL,poll_outcome=? WHERE request_id=?", []byte(`{}`), id); err != nil {
			t.Fatalf("single poll UPDATE control %s: %v", column, err)
		}
	}
}
