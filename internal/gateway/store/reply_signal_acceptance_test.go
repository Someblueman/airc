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

func replySignalStoreFixture(t *testing.T) (config.Config, auth.Identity) {
	t.Helper()
	c, _ := activityFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.ReplySignalSet, core.ReplySignalCancel)
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

func replySignalStoreRequest(op core.Operation) core.Request {
	r := core.Request{Version: 1, RequestID: "signal-request", Operation: op, Target: &core.Target{Kind: "room", Name: "#Allowed"}, ParentID: "0123456789abcdef0123456789abcdef"}
	if op == core.ReplySignalSet {
		r.ReplySignal = &core.ReplySignalInput{Note: "requested", TTLSeconds: 120}
	}
	return r
}

func replySignalStoreAck(op core.Operation) *core.ReplySignalAcknowledgement {
	r := replySignalStoreRequest(op)
	a := &core.ReplySignalAcknowledgement{Version: 1, Operation: op, Target: *r.Target, ParentID: r.ParentID, NativeTarget: r.Target.Name, Action: "cancel", From: "author", ExpiresAt: time.Unix(1001, 0).UTC()}
	if op == core.ReplySignalSet {
		a.Action = "prepare"
		a.Note = "actual previous note"
		a.AccountID = "0123456789abcdef0123456789abcdef"
	}
	return a
}

func TestReplySignalAcceptanceRestartCopiesIntentAndRetention(t *testing.T) {
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		t.Run(string(op), func(t *testing.T) {
			c, i := replySignalStoreFixture(t)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			r := replySignalStoreRequest(op)
			if _, _, err = s.Admit(ctx, i, r, 0, now); err != nil {
				t.Fatal(err)
			}
			r.Target.Name = "#CallerChanged"
			if r.ReplySignal != nil {
				r.ReplySignal.Note = "caller changed"
			}
			g, err := s.Reserve(ctx, i.ID(), r.RequestID, SendAttempt, now)
			if err != nil {
				t.Fatal(err)
			}
			a, err := g.Consume(now)
			if err != nil {
				t.Fatal(err)
			}
			if *a.Job.Request.Target != *replySignalStoreRequest(op).Target || a.Job.Request.ReplySignal != nil && a.Job.Request.ReplySignal.Note != "requested" {
				t.Fatal("caller changed admitted intent", a)
			}
			a.Job.Request.Target.Name = "#BackendChanged"
			if a.Job.Request.ReplySignal != nil {
				a.Job.Request.ReplySignal.Note = "backend changed"
			}
			ack := replySignalStoreAck(op)
			if _, err = s.RecordReplySignalAcceptance(ctx, g, ack, now, now); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			j, err := s.Lookup(ctx, i, r.RequestID)
			if err != nil || j.Result.ReplySignal == nil || *j.Result.ReplySignal != *ack || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.State != core.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Receipt != nil || j.Result.Card != nil || j.Result.Topic != nil || j.Result.Room != nil || j.Result.Pin != nil || j.Result.Activity != nil || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil {
				t.Fatalf("whole reply signal lost: %+v %v", j.Result, err)
			}
			if _, err = s.Reserve(ctx, i.ID(), r.RequestID, SendAttempt, now); !errors.Is(err, ErrNotReady) {
				t.Fatalf("accepted signal resent: %v", err)
			}
			if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Lookup(ctx, i, r.RequestID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("signal retention not pruned: %v", err)
			}
		})
	}
}

func TestReplySignalAcceptanceSaveFailureIsImmutableAndFencesCompletion(t *testing.T) {
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
			if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_signal BEFORE UPDATE OF reply_signal_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
				t.Fatal(err)
			}
			ack := replySignalStoreAck(op)
			if _, err = s.RecordReplySignalAcceptance(ctx, g, ack, now, now); err == nil {
				t.Fatal("failed save claimed acceptance")
			}
			if _, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); !errors.Is(err, ErrNotReady) {
				t.Fatalf("completion erased evidence: %v", err)
			}
			ack.ExpiresAt = ack.ExpiresAt.Add(time.Second)
			if _, err = s.RecordReplySignalAcceptance(ctx, g, ack, now, now); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed evidence overwrote private ACK: %v", err)
			}
			if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_signal"); err != nil {
				t.Fatal(err)
			}
			j, err := s.RetryAcceptance(ctx, g, now)
			if err != nil || j.Result.ReplySignal == nil || *j.Result.ReplySignal != *replySignalStoreAck(op) {
				t.Fatalf("save retry changed actual ACK: %+v %v", j.Result, err)
			}
		})
	}
}
