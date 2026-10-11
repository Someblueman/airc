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

func TestPollAcceptanceSaveFailureKeepsPrivateEvidence(t *testing.T) {
	for _, op := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
		t.Run(string(op), func(t *testing.T) {
			c, identity := pollFixture(t)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := pollRequest(op)
			if _, _, err = s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = g.Consume(now); err != nil {
				t.Fatal(err)
			}
			ack := pollAck(r, op)
			if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_poll_failure BEFORE UPDATE OF poll_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.RecordPollAcceptance(ctx, g, ack, now, now); err == nil {
				t.Fatal("poll save unexpectedly succeeded")
			}
			if _, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); !errors.Is(err, ErrNotReady) {
				t.Fatalf("completion erased observed poll: %v", err)
			}
			ack.Poll.Votes[0]++
			if ack.Receipt != nil {
				ack.Receipt.Receipt.Persisted = false
				ack.Receipt.PollOptions[0] = "caller mutation"
			}
			if _, err = s.RecordPollAcceptance(ctx, g, ack, now, now); err == nil {
				t.Fatal("replaced observed poll evidence")
			}
			if _, err = s.db.ExecContext(ctx, `DROP TRIGGER fixture_poll_failure`); err != nil {
				t.Fatal(err)
			}
			j, err := s.RetryAcceptance(ctx, g, now)
			if err != nil || !reflect.DeepEqual(j.Result.Poll, pollAck(r, op)) {
				t.Fatalf("private poll evidence changed: %+v error=%v", j.Result, err)
			}
		})
	}
}
