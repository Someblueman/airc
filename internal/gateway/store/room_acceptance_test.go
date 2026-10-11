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

func roomFixture(t *testing.T) (config.Config, Submission) {
	t.Helper()
	c, _ := cardFixture(t)
	i := fixtureAuthor()
	return c, i
}

func roomRequest() core.Request {
	slow, history := int64(5), 100
	return core.Request{Version: 1, RequestID: "room-request", Operation: core.RoomUpdate, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Room: &core.RoomPatch{SlowSeconds: &slow, HistoryLimit: &history}}
}

func roomAck() *core.RoomAcknowledgement {
	return &core.RoomAcknowledgement{Version: 1, Operation: core.RoomUpdate, Room: core.RoomResult{Target: core.Target{Kind: "room", Name: "#Allowed"}, SlowSeconds: 5, HistoryLimit: 100}}
}

func TestRoomAcceptancePersistsAndHasNoMessageFacts(t *testing.T) {
	c, identity := roomFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, roomRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	var limit int
	if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs WHERE request_id='room-request'").Scan(&limit); err != nil || limit != 0 {
		t.Fatalf("lookup budget %d: %v", limit, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "room-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordRoomAcceptance(ctx, g, roomAck(), now, now); err != nil {
		t.Fatal(err)
	}
	j, err := s.LookupAuthor(ctx, identity, "room-request")
	if err != nil || j.Result.Room == nil || j.Result.Room.Room.HistoryLimit != 100 || j.Receipt != nil || j.Result.Persisted != nil || j.Result.RecipientConnected != nil || j.Result.MessageID != "" {
		t.Fatalf("room outcome malformed: %#v %v", j, err)
	}
}

func TestRoomAcceptanceSurvivesRestartAndCannotResend(t *testing.T) {
	c, identity := roomFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitAuthor(ctx, identity, roomRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "room-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	ack := roomAck()
	raw, err := core.RoomAckBytes(ack, roomRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordRoomAcceptance(ctx, g, ack, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "room-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("accepted room resent: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.LookupAuthor(ctx, identity, "room-request")
	var retained []byte
	if queryErr := s.db.QueryRowContext(ctx, "SELECT room_outcome FROM jobs WHERE request_id='room-request'").Scan(&retained); queryErr != nil || string(retained) != string(raw) || err != nil || j.Result.Room == nil || j.Result.Room.Room.SlowSeconds != 5 || j.Result.Room.Room.HistoryLimit != 100 || j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil {
		t.Fatalf("room facts changed on restart: %#v %v", j, err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "room-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("restart resent accepted room: %v", err)
	}
}

func TestRoomGrantConsumptionClonesBothPatchPointers(t *testing.T) {
	c, identity := roomFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := roomRequest()
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
	*a.Job.Request.Room.SlowSeconds = 99
	*a.Job.Request.Room.HistoryLimit = 1
	if _, err = s.RecordRoomAcceptance(ctx, g, roomAck(), now, now); err != nil {
		t.Fatalf("room private pointers changed: %v", err)
	}
}

func TestRoomAcceptanceRetentionPrunesResult(t *testing.T) {
	c, identity := roomFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitAuthor(ctx, identity, roomRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "room-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordRoomAcceptance(ctx, g, roomAck(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupAuthor(ctx, identity, "room-request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("room result not pruned: %v", err)
	}
	s.Close()
}

func TestRoomAcceptanceRetryAndExclusivity(t *testing.T) {
	c, identity := roomFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, roomRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "room-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_room BEFORE UPDATE OF room_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	ack := roomAck()
	if _, err = s.RecordRoomAcceptance(ctx, g, ack, now, now); err == nil {
		t.Fatal("failed save claimed room")
	}
	ack.Room.HistoryLimit = 1
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_room"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Result.Room == nil || j.Result.Room.Room.HistoryLimit != 100 {
		t.Fatalf("immutable retry failed: %#v %v", j, err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET receipt=?,card_outcome=?,topic_outcome=?,room_outcome=? WHERE request_id='room-request'", []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`)); err == nil {
		t.Fatal("accepted four simultaneous outcomes")
	}
}

func TestRoomSaveFailureRestartPolicies(t *testing.T) {
	for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
		t.Run(string(policy), func(t *testing.T) {
			c, identity := roomFixture(t)
			ctx := context.Background()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			r := roomRequest()
			r.DeliveryPolicy = &policy
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
			if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_room_restart BEFORE UPDATE OF room_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.RecordRoomAcceptance(ctx, g, roomAck(), now, now); err == nil {
				t.Fatal("failed room save claimed acceptance")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			j, err := s.LookupAuthor(ctx, identity, r.RequestID)
			if err != nil || !j.Result.AcceptanceUncertain {
				t.Fatalf("restart lost uncertainty: %#v %v", j, err)
			}
			if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, LookupAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
				t.Fatalf("room spent lookup budget: %v", err)
			}
			if policy == core.Retry {
				if !j.Result.DuplicateRisk || j.Result.NextAction != "send" {
					t.Fatalf("retry state: %#v", j)
				}
				if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			} else if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
				t.Fatalf("receipt-only room resent: %v", err)
			}
		})
	}
}

func TestRoomOutcomePairwiseExclusivity(t *testing.T) {
	c, identity := roomFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, roomRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{{"receipt", "room_outcome"}, {"card_outcome", "room_outcome"}, {"topic_outcome", "room_outcome"}}
	for _, pair := range pairs {
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+pair[0]+"=?, "+pair[1]+"=? WHERE request_id='room-request'", []byte(`{}`), []byte(`{}`)); err == nil {
			t.Fatalf("accepted pair %v", pair)
		}
	}
}
