//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestReplySignalAcceptanceWrongIntentAndDeadline(t *testing.T) {
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		t.Run(string(op), func(t *testing.T) {
			c, i := replySignalStoreFixture(t)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := replySignalStoreRequest(op)
			if _, _, err = s.Admit(ctx, i, r, 0, now); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, i.ID(), r.RequestID, SendAttempt, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = g.Consume(now); err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func(*core.ReplySignalAcknowledgement){func(a *core.ReplySignalAcknowledgement) { a.Target.Name = "#Foreign" }, func(a *core.ReplySignalAcknowledgement) { a.ParentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }, func(a *core.ReplySignalAcknowledgement) { a.From = "foreign" }, func(a *core.ReplySignalAcknowledgement) { a.NativeTarget = "#Foreign" }} {
				a := replySignalStoreAck(op)
				mutate(a)
				if _, err = s.RecordReplySignalAcceptance(ctx, g, a, now, now); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted wrong signal intent: %v", err)
				}
			}
			if _, err = s.RecordReplySignalAcceptance(ctx, g, replySignalStoreAck(op), now.Add(31*time.Second), now.Add(31*time.Second)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted signal after deadline: %v", err)
			}
		})
	}
}

func TestReplySignalOutcomeInsertUpdateExclusivity(t *testing.T) {
	c, i := replySignalStoreFixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := replySignalStoreRequest(core.ReplySignalSet)
	if _, _, err = s.Admit(ctx, i, r, 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"receipt", "card_outcome", "topic_outcome", "room_outcome", "pin_outcome", "activity_outcome"} {
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+column+"=?,reply_signal_outcome=? WHERE request_id=?", []byte(`{}`), []byte(`{}`), r.RequestID); err == nil {
			t.Fatalf("UPDATE allowed reply signal with %s", column)
		}
		id := "signal-insert-" + column
		upstream, err := core.UpstreamID(c.Realm, i.Binding(), i.ID(), id)
		if err != nil {
			t.Fatal(err)
		}
		query := "INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at," + column + ",reply_signal_outcome) SELECT principal,binding,?,fingerprint,?,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,?,? FROM jobs WHERE request_id=?"
		if _, err = s.db.ExecContext(ctx, query, id, upstream, []byte(`{}`), []byte(`{}`), r.RequestID); err == nil {
			t.Fatalf("INSERT allowed reply signal with %s", column)
		}
		if _, err = s.db.ExecContext(ctx, query, id, upstream, []byte(`{}`), nil, r.RequestID); err != nil {
			t.Fatalf("single prior-outcome INSERT %s: %v", column, err)
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+column+"=NULL,reply_signal_outcome=? WHERE request_id=?", []byte(`{}`), id); err != nil {
			t.Fatalf("single signal UPDATE control %s: %v", column, err)
		}
	}
}
