//go:build darwin || linux

package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMessageActionUsesOrdinaryReceiptAndNoLookup(t *testing.T) {
	c, _ := cardFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.MessageAction, core.MessageCorrect, core.MessageRetract)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "message-action", Operation: core.MessageAction, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Message: "announcement"}
	if _, _, err = s.Admit(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	var lookup int
	if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs WHERE request_id=?", r.RequestID).Scan(&lookup); err != nil || lookup != 0 {
		t.Fatalf("lookup limit=%d err=%v", lookup, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	receipt := &irc.SendReceiptEvent{ChatMetadata: irc.ChatMetadata{AccountID: strings.Repeat("a", 32), Kind: "action"}, Type: "send_receipt", ID: strings.Repeat("b", 32), Seq: 1, From: "author", Target: "#Allowed", Message: r.Message, Timestamp: time.Unix(1001, 0), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: true}}
	if _, err = core.ReceiptBytes(receipt, r, "ignored", "author"); err != nil {
		t.Fatalf("receipt validation: %v target=%+v request=%#v receipt=%#v", err, *r.Target, r, receipt)
	}
	if _, err = s.RecordAcceptance(ctx, g, receipt, now, now); err != nil {
		t.Fatal(err)
	}
	j, err := s.Lookup(ctx, identity, r.RequestID)
	if err != nil || j.Receipt == nil || j.Receipt.Kind != "action" || j.Result.MessageID != receipt.ID || j.Result.Persisted == nil || !*j.Result.Persisted || j.Result.LookupAttempts != 0 {
		t.Fatalf("action result=%#v err=%v", j, err)
	}
}

func TestMessageMutationsReplayAllNativeReceiptsAfterRestart(t *testing.T) {
	c, _ := cardFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.MessageAction, core.MessageCorrect, core.MessageRetract)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"message-action-replay", "message-correct-replay", "message-retract-replay"}
	parent := strings.Repeat("a", 32)
	ops := []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract}
	want := make(map[string]string, len(ops))
	for i, op := range ops {
		r := core.Request{Version: 1, RequestID: ids[i], Operation: op, Target: &core.Target{Kind: "room", Name: "#Allowed"}}
		if op == core.MessageAction {
			r.Message = "announcement"
		} else {
			r.ParentID = parent
			if op == core.MessageCorrect {
				r.Message = "replacement"
			}
		}
		if _, _, err = s.Admit(ctx, identity, r, 0, now); err != nil {
			t.Fatal(op, err)
		}
		g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
		if err != nil {
			t.Fatal(op, err)
		}
		if _, err = g.Consume(now); err != nil {
			t.Fatal(op, err)
		}
		kind, body := "action", r.Message
		if op == core.MessageCorrect {
			kind, body = "correct", "replacement"
		}
		if op == core.MessageRetract {
			kind, body = "retract", "Retracted"
		}
		id := strings.Repeat(string('b'+rune(i)), 32)
		receipt := &irc.SendReceiptEvent{ChatMetadata: irc.ChatMetadata{AccountID: strings.Repeat("a", 32), Kind: kind, Supersedes: r.ParentID}, Type: "send_receipt", ID: id, Seq: uint64(i + 1), From: "author", Target: "#Allowed", Message: body, ReplyTo: r.ParentID, ThreadID: parent, Timestamp: now.Add(time.Duration(i+1) * time.Second), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: i != 1}}
		if op == core.MessageAction {
			receipt.ReplyTo, receipt.ThreadID = "", ""
		}
		raw, err := core.ReceiptBytes(receipt, r, "ignored", "author")
		if err != nil {
			t.Fatal(op, err)
		}
		want[r.RequestID] = string(raw)
		if _, err = s.RecordAcceptance(ctx, g, receipt, now, now); err != nil {
			t.Fatal(op, err)
		}
		var stored []byte
		if err = s.db.QueryRowContext(ctx, "SELECT receipt FROM jobs WHERE request_id=?", r.RequestID).Scan(&stored); err != nil || string(stored) != string(raw) {
			t.Fatalf("%s canonical receipt: %v", op, err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, op := range ops {
		j, err := s.Lookup(ctx, identity, ids[i])
		if err != nil || j.Receipt == nil || j.Receipt.Kind == "" || j.Result.LookupAttempts != 0 || j.Result.Accepted == nil || !*j.Result.Accepted {
			t.Fatalf("%s replay: %#v %v", op, j, err)
		}
		replay, marshalErr := json.Marshal(j.Receipt)
		if marshalErr != nil || string(replay) != want[ids[i]] || j.Result.MessageID != j.Receipt.ID || j.Result.Persisted == nil || *j.Result.Persisted != (i != 1) {
			t.Fatalf("%s changed whole canonical receipt after restart: %+v error=%v", op, j.Result, marshalErr)
		}
		if _, err = s.Reserve(ctx, scopeOf(s, identity), ids[i], SendAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
			t.Fatalf("accepted mutation became ready again: %v", err)
		}
	}
}

func TestMessageCorrectionSaveFailureKeepsPrivateReceipt(t *testing.T) {
	c, _ := cardFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.MessageCorrect)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "message-correct-save", Operation: core.MessageCorrect, Target: &core.Target{Kind: "room", Name: "#Allowed"}, ParentID: strings.Repeat("a", 32), Message: "replacement"}
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
	receipt := &irc.SendReceiptEvent{ChatMetadata: irc.ChatMetadata{AccountID: strings.Repeat("a", 32), Kind: "correct", Supersedes: r.ParentID}, Type: "send_receipt", ID: strings.Repeat("c", 32), Seq: 1, From: "author", Target: "#Allowed", Message: r.Message, ReplyTo: r.ParentID, ThreadID: r.ParentID, Timestamp: now.Add(time.Second), Receipt: &irc.ReceiptInfo{Accepted: true}}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_message_failure BEFORE UPDATE OF receipt ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordAcceptance(ctx, g, receipt, now, now); err == nil {
		t.Fatal("correction save unexpectedly succeeded")
	}
	if _, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("completion erased receipt evidence: %v", err)
	}
	receipt.Message = "caller mutation"
	if _, err = s.db.ExecContext(ctx, `DROP TRIGGER fixture_message_failure`); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Receipt == nil || j.Receipt.Message != "replacement" {
		t.Fatalf("correction retry changed receipt: %#v %v", j, err)
	}
}
