//go:build darwin || linux

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func pollFixture(t *testing.T) (config.Config, auth.Identity) {
	t.Helper()
	c, _ := roomFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.PollCreate, core.PollVote, core.PollClose)
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

func pollRequest(op core.Operation) core.Request {
	r := core.Request{Version: 1, RequestID: "poll-" + strings.ReplaceAll(string(op), ".", "-"), Operation: op, Target: &core.Target{Kind: "room", Name: "#Allowed"}}
	if op == core.PollCreate {
		r.Poll = &core.PollInput{Question: "choose", Options: []string{"one", "two"}, TTLSeconds: 60}
	} else {
		r.PollID = strings.Repeat("a", 32)
		if op == core.PollVote {
			r.Choice = 1
		}
	}
	return r
}

func pollAck(r core.Request, op core.Operation) *core.PollAcknowledgement {
	id := strings.Repeat("a", 32)
	account := strings.Repeat("b", 32)
	votes := []int{1, 0}
	closed := false
	if op == core.PollCreate {
		votes = []int{0, 0}
	}
	if op == core.PollClose {
		closed = true
	}
	result := core.PollResult{Target: *r.Target, ID: id, From: "author", AccountID: account, Question: "choose", Options: []string{"one", "two"}, Votes: votes, ExpiresAt: time.Unix(1060, 0), Closed: closed}
	a := &core.PollAcknowledgement{Version: 1, Operation: op, Target: *r.Target, Action: "results", Poll: result}
	if op == core.PollCreate {
		a.Receipt = &irc.SendReceiptEvent{ChatMetadata: irc.ChatMetadata{AccountID: account, Kind: "poll", PollOptions: []string{"one", "two"}, PollClosesAt: time.Unix(1060, 0)}, Type: "send_receipt", ID: id, From: "author", Target: r.Target.Name, Message: "choose", Timestamp: time.Unix(1001, 0), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: true}}
	}
	return a
}

func TestPollAcceptanceCreateVoteCloseFacts(t *testing.T) {
	c, identity := pollFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, op := range []core.Operation{core.PollCreate, core.PollVote, core.PollClose} {
		r := pollRequest(op)
		r.RequestID += "-facts"
		if err := r.Normalize(); err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.Admit(ctx, identity, r, 0, now); err != nil {
			t.Fatal(op, err)
		}
		if r.Poll != nil {
			r.Poll.Options[0] = "caller-mutated"
		}
		g, err := s.Reserve(ctx, identity.ID(), r.RequestID, SendAttempt, now)
		if err != nil {
			t.Fatal(op, err)
		}
		if _, err = g.Consume(now); err != nil {
			t.Fatal(op, err)
		}
		if op == core.PollCreate && g.state.attempt.Job.Request.Poll.Options[0] != "one" {
			t.Fatal("poll options leaked through admission")
		}
		if r.Poll != nil {
			r.Poll.Options[0] = "one"
		}
		ack := pollAck(r, op)
		raw, err := core.PollAckBytes(ack, r, "author")
		if err != nil {
			t.Fatal(op, err)
		}
		if _, err = s.RecordPollAcceptance(ctx, g, ack, now, now); err != nil {
			t.Fatal(op, err)
		}
		var stored []byte
		if err = s.db.QueryRowContext(ctx, "SELECT poll_outcome FROM jobs WHERE request_id=?", r.RequestID).Scan(&stored); err != nil || string(stored) != string(raw) {
			t.Fatalf("%s bytes: %v", op, err)
		}
		j, err := s.Lookup(ctx, identity, r.RequestID)
		if err != nil || j.Result.Poll == nil || j.Result.Accepted == nil || !*j.Result.Accepted || j.Receipt != nil {
			t.Fatalf("%s result: %#v %v", op, j, err)
		}
		if op == core.PollCreate && (j.Result.MessageID == "" || j.Result.Persisted == nil) {
			t.Fatalf("create receipt facts missing: %#v", j.Result)
		}
		if op != core.PollCreate && (j.Result.MessageID != "" || j.Result.Persisted != nil || j.Result.RecipientConnected != nil) {
			t.Fatalf("%s fabricated message facts: %#v", op, j.Result)
		}
	}
}
