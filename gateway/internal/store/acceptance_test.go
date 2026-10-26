//go:build darwin || linux

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// acceptanceCase is one mutation's journal fixture: the request, the
// evidence AIRC acknowledges it with (for the author "author"), and changes
// that break the evidence's correlation with the request.
type acceptanceCase struct {
	request  func() core.Request
	evidence func(upstreamID string) core.Acceptance
	wrong    []func(*core.Acceptance)
}

const (
	fixtureParent  = "0123456789abcdef0123456789abcdef"
	fixtureAccount = "fedcba9876543210fedcba9876543210"
)

func fixtureRoom() *core.Target { return &core.Target{Kind: "room", Name: "#Allowed"} }

func fixtureReceipt(r core.Request, upstreamID string) *irc.SendReceiptEvent {
	connected := false
	return &irc.SendReceiptEvent{Type: "send_receipt", ChatMetadata: irc.ChatMetadata{RequestID: upstreamID, AccountID: "fixture-account"}, ID: strings.Repeat("a", 32), From: "author", Target: r.Target.Name, Message: r.Message, Timestamp: time.Unix(1000, 123), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: false, RecipientConnected: &connected}}
}

func messageMutationCase(op core.Operation) acceptanceCase {
	request := func() core.Request {
		r := core.Request{Version: 1, RequestID: "request", Operation: op, Target: fixtureRoom(), Message: "announcement"}
		if op != core.MessageAction {
			r.ParentID = fixtureParent
		}
		if op == core.MessageRetract {
			r.Message = ""
		}
		return r
	}
	return acceptanceCase{request: request, evidence: func(string) core.Acceptance {
		r := request()
		kind, body, thread := "action", r.Message, ""
		if op == core.MessageCorrect {
			kind, thread = "correct", fixtureParent
		} else if op == core.MessageRetract {
			kind, body, thread = "retract", "Retracted", fixtureParent
		}
		return core.Acceptance{Receipt: &irc.SendReceiptEvent{ChatMetadata: irc.ChatMetadata{AccountID: fixtureAccount, Kind: kind, Supersedes: r.ParentID}, Type: "send_receipt", ID: strings.Repeat("b", 32), Seq: 1, From: "author", Target: "#Allowed", Message: body, ReplyTo: r.ParentID, ThreadID: thread, Timestamp: time.Unix(1001, 0), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: op != core.MessageCorrect}}}
	}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Receipt.Kind = "other" }, func(a *core.Acceptance) { a.Receipt.Message = "different" }}}
}

func cardCase(op core.Operation) acceptanceCase {
	request := func() core.Request {
		if op == core.PresenceSet {
			return core.Request{Version: 1, RequestID: "request", Operation: op, Presence: &core.PresenceInput{State: "away", Note: "busy", TTLSeconds: 60}}
		}
		model := "model"
		return core.Request{Version: 1, RequestID: "request", Operation: op, Profile: &core.ProfilePatch{Model: &model}}
	}
	return acceptanceCase{request: request, evidence: func(string) core.Acceptance {
		card := irc.AgentCard{Nick: "author", AccountID: fixtureAccount, Model: "model", State: "unknown", LastSeen: time.Unix(1000, 0).UTC()}
		if op == core.PresenceSet {
			card = irc.AgentCard{Nick: "author", AccountID: fixtureAccount, State: "away", Note: "busy", LastSeen: time.Unix(1000, 0).UTC(), ExpiresAt: time.Unix(1060, 0).UTC()}
		}
		return core.Acceptance{Card: &core.CardAcknowledgement{Version: 1, Operation: op, Card: card}}
	}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Card.Card.Nick = "other" }, func(a *core.Acceptance) { a.Card.Card.AccountID = "" }}}
}

func pinCase(op core.Operation) acceptanceCase {
	return acceptanceCase{request: func() core.Request {
		return core.Request{Version: 1, RequestID: "request", Operation: op, Target: fixtureRoom(), ParentID: fixtureParent}
	}, evidence: func(string) core.Acceptance {
		return core.Acceptance{Pin: &core.PinAcknowledgement{Version: 1, Operation: op, Target: *fixtureRoom(), ParentID: fixtureParent}}
	}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Pin.ParentID = strings.Repeat("a", 32) }}}
}

func replySignalCase(op core.Operation) acceptanceCase {
	request := func() core.Request {
		r := core.Request{Version: 1, RequestID: "request", Operation: op, Target: fixtureRoom(), ParentID: fixtureParent}
		if op == core.ReplySignalSet {
			r.ReplySignal = &core.ReplySignalInput{Note: "requested", TTLSeconds: 120}
		}
		return r
	}
	return acceptanceCase{request: request, evidence: func(string) core.Acceptance {
		a := &core.ReplySignalAcknowledgement{Version: 1, Operation: op, Target: *fixtureRoom(), ParentID: fixtureParent, NativeTarget: "#Allowed", Action: "cancel", From: "author", ExpiresAt: time.Unix(1001, 0).UTC()}
		if op == core.ReplySignalSet {
			a.Action, a.Note, a.AccountID = "prepare", "actual previous note", fixtureAccount
		}
		return core.Acceptance{ReplySignal: a}
	}, wrong: []func(*core.Acceptance){
		func(a *core.Acceptance) { a.ReplySignal.Target.Name = "#Foreign" },
		func(a *core.Acceptance) { a.ReplySignal.ParentID = strings.Repeat("a", 32) },
		func(a *core.Acceptance) { a.ReplySignal.From = "foreign" },
		func(a *core.Acceptance) { a.ReplySignal.NativeTarget = "#Foreign" },
	}}
}

func pollCase(op core.Operation) acceptanceCase {
	id := strings.Repeat("a", 32)
	request := func() core.Request {
		r := core.Request{Version: 1, RequestID: "request", Operation: op, Target: fixtureRoom()}
		if op == core.PollCreate {
			r.Poll = &core.PollInput{Question: "choose", Options: []string{"one", "two"}, TTLSeconds: 60}
		} else {
			r.PollID = id
			if op == core.PollVote {
				r.Choice = 1
			}
		}
		return r
	}
	return acceptanceCase{request: request, evidence: func(string) core.Acceptance {
		votes := []int{1, 0}
		if op == core.PollCreate {
			votes = []int{0, 0}
		}
		result := core.PollResult{Target: *fixtureRoom(), ID: id, From: "author", AccountID: fixtureAccount, Question: "choose", Options: []string{"one", "two"}, Votes: votes, ExpiresAt: time.Unix(1060, 0).UTC(), Closed: op == core.PollClose}
		a := &core.PollAcknowledgement{Version: 1, Operation: op, Target: *fixtureRoom(), Action: "results", Poll: result}
		if op == core.PollCreate {
			// A false persistence fact must survive as such.
			a.Receipt = &irc.SendReceiptEvent{ChatMetadata: irc.ChatMetadata{AccountID: fixtureAccount, Kind: "poll", PollOptions: []string{"one", "two"}, PollClosesAt: time.Unix(1060, 0).UTC()}, Type: "send_receipt", ID: id, From: "author", Target: "#Allowed", Message: "choose", Timestamp: time.Unix(1001, 0).UTC(), Receipt: &irc.ReceiptInfo{Accepted: true, Persisted: false}}
		}
		return core.Acceptance{Poll: a}
	}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Poll.Poll.ID = strings.Repeat("c", 32) }, func(a *core.Acceptance) { a.Poll.Target.Name = "#Other" }}}
}

// acceptanceCases covers every durable operation.
func acceptanceCases() map[core.Operation]acceptanceCase {
	reply := func() core.Request {
		return core.Request{Version: 1, RequestID: "request", Operation: core.Reply, Target: fixtureRoom(), ParentID: fixtureParent, Message: "answer"}
	}
	reaction := func() core.Request {
		return core.Request{Version: 1, RequestID: "request", Operation: core.ReactionAdd, Target: fixtureRoom(), ParentID: fixtureParent, Reaction: "seen"}
	}
	topic := func() core.Request {
		topic := "hello"
		return core.Request{Version: 1, RequestID: "request", Operation: core.TopicSet, Target: fixtureRoom(), Topic: &topic}
	}
	room := func() core.Request {
		slow, history := int64(5), 100
		return core.Request{Version: 1, RequestID: "request", Operation: core.RoomUpdate, Target: fixtureRoom(), Room: &core.RoomPatch{SlowSeconds: &slow, HistoryLimit: &history}}
	}
	activity := func() core.Request {
		return core.Request{Version: 1, RequestID: "request", Operation: core.ActivitySet, Target: fixtureRoom(), Activity: &core.ActivityInput{Kind: "typing", Note: "working", TTLSeconds: 5}}
	}
	send := func() core.Request {
		r := request()
		r.Message = "first\nsecond" // As admission normalizes it.
		return r
	}
	return map[core.Operation]acceptanceCase{
		core.Send: {request: send, evidence: func(upstreamID string) core.Acceptance {
			return core.Acceptance{Receipt: fixtureReceipt(send(), upstreamID)}
		}, wrong: []func(*core.Acceptance){
			func(a *core.Acceptance) { a.Receipt.RequestID = "different" },
			func(a *core.Acceptance) { a.Receipt.Target = "#Denied" },
			func(a *core.Acceptance) { a.Receipt.From = "stranger" },
			func(a *core.Acceptance) { a.Receipt.Message = "different" },
			func(a *core.Acceptance) { a.Receipt.Receipt = nil },
			func(a *core.Acceptance) { a.Receipt.Receipt.Accepted = false },
			func(a *core.Acceptance) { a.Receipt.ReplyTo = strings.Repeat("b", 32) },
		}},
		core.Reply: {request: reply, evidence: func(upstreamID string) core.Acceptance {
			r := fixtureReceipt(reply(), upstreamID)
			r.ReplyTo, r.ThreadID = fixtureParent, fixtureParent
			return core.Acceptance{Receipt: r}
		}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Receipt.ReplyTo = strings.Repeat("b", 32) }, func(a *core.Acceptance) { a.Receipt.ThreadID = "" }}},
		core.ReactionAdd: {request: reaction, evidence: func(string) core.Acceptance {
			r := fixtureReceipt(reaction(), "")
			r.Message, r.Reaction, r.ReplyTo, r.ThreadID = "seen", "seen", fixtureParent, fixtureParent
			return core.Acceptance{Receipt: r}
		}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Receipt.Reaction = "agree" }, func(a *core.Acceptance) { a.Receipt.RequestID = "request" }}},
		core.MessageAction:  messageMutationCase(core.MessageAction),
		core.MessageCorrect: messageMutationCase(core.MessageCorrect),
		core.MessageRetract: messageMutationCase(core.MessageRetract),
		core.ProfileUpdate:  cardCase(core.ProfileUpdate),
		core.PresenceSet:    cardCase(core.PresenceSet),
		core.TopicSet: {request: topic, evidence: func(string) core.Acceptance {
			return core.Acceptance{Topic: &core.TopicAcknowledgement{Version: 1, Operation: core.TopicSet, Topic: core.TopicResult{Target: *fixtureRoom(), Topic: "hello", SetBy: "author", SetAt: time.Unix(1000, 0).UTC()}}}
		}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Topic.Topic.Topic = "other" }, func(a *core.Acceptance) { a.Topic.Topic.SetBy = "other" }}},
		core.RoomUpdate: {request: room, evidence: func(string) core.Acceptance {
			return core.Acceptance{Room: &core.RoomAcknowledgement{Version: 1, Operation: core.RoomUpdate, Room: core.RoomResult{Target: *fixtureRoom(), SlowSeconds: 5, HistoryLimit: 100}}}
		}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Room.Room.HistoryLimit = 1 }}},
		core.PinsAdd:    pinCase(core.PinsAdd),
		core.PinsRemove: pinCase(core.PinsRemove),
		core.ActivitySet: {request: activity, evidence: func(string) core.Acceptance {
			return core.Acceptance{Activity: &core.ActivityAcknowledgement{Version: 1, Operation: core.ActivitySet, Target: *fixtureRoom(), Kind: "thinking", From: "author", AccountID: fixtureAccount, Note: "native", ExpiresAt: time.Unix(1010, 0).UTC()}}
		}, wrong: []func(*core.Acceptance){func(a *core.Acceptance) { a.Activity.Target.Name = "#Other" }, func(a *core.Acceptance) { a.Activity.From = "other" }}},
		core.ReplySignalSet:    replySignalCase(core.ReplySignalSet),
		core.ReplySignalCancel: replySignalCase(core.ReplySignalCancel),
		core.PollCreate:        pollCase(core.PollCreate),
		core.PollVote:          pollCase(core.PollVote),
		core.PollClose:         pollCase(core.PollClose),
	}
}

// variant changes evidence into other valid evidence for the same request,
// when its kind has a field AIRC chooses freely.
func variant(a *core.Acceptance) bool {
	switch {
	case a.Receipt != nil:
		a.Receipt.Timestamp = a.Receipt.Timestamp.Add(time.Second)
	case a.Card != nil:
		a.Card.Card.LastSeen = a.Card.Card.LastSeen.Add(time.Second)
		if !a.Card.Card.ExpiresAt.IsZero() {
			a.Card.Card.ExpiresAt = a.Card.Card.ExpiresAt.Add(time.Second)
		}
	case a.Topic != nil:
		a.Topic.Topic.SetAt = a.Topic.Topic.SetAt.Add(time.Second)
	case a.Activity != nil:
		a.Activity.ExpiresAt = a.Activity.ExpiresAt.Add(time.Second)
	case a.ReplySignal != nil:
		a.ReplySignal.ExpiresAt = a.ReplySignal.ExpiresAt.Add(time.Second)
	case a.Poll != nil && a.Poll.Receipt != nil:
		a.Poll.Receipt.Timestamp = a.Poll.Receipt.Timestamp.Add(time.Second)
	case a.Poll != nil:
		a.Poll.Poll.Votes[0]++
	default:
		return false
	}
	return true
}

// otherKind is evidence of a kind op never produces.
func otherKind(op core.Operation) core.Acceptance {
	if op.HasLookup() || op == core.MessageAction || op == core.MessageCorrect || op == core.MessageRetract {
		return cardCase(core.ProfileUpdate).evidence("")
	}
	return core.Acceptance{Receipt: fixtureReceipt(request(), "")}
}

// scribble changes everything a request holds behind a pointer. The store's
// private intent must not alias a caller's or a backend's copy.
func scribble(r *core.Request) {
	if r.Target != nil {
		r.Target.Name = "#Scribbled"
	}
	if r.Profile != nil && r.Profile.Model != nil {
		*r.Profile.Model = "scribbled"
	}
	if r.Presence != nil {
		r.Presence.Note = "scribbled"
	}
	if r.Topic != nil {
		*r.Topic = "scribbled"
	}
	if r.Room != nil {
		*r.Room.SlowSeconds, *r.Room.HistoryLimit = 99, 1
	}
	if r.Activity != nil {
		r.Activity.Note = "scribbled"
	}
	if r.ReplySignal != nil {
		r.ReplySignal.Note = "scribbled"
	}
	if r.Poll != nil {
		r.Poll.Options[0] = "scribbled"
	}
}

// consumed admits r, with policy if one is given, then changes the caller's
// copy and consumes the job's first send grant.
func consumed(t *testing.T, s *Store, identity Submission, r core.Request, policy core.Policy, now time.Time) (*Grant, Attempt) {
	t.Helper()
	if policy != "" {
		r.DeliveryPolicy = &policy
	}
	ctx := t.Context()
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	scribble(&r)
	g, err := s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	return g, a
}

// checkAccepted asserts an accepted job holds exactly the encoded evidence
// want, with message facts only from a message's receipt.
func checkAccepted(t *testing.T, tc acceptanceCase, j Job, want []byte) {
	t.Helper()
	if j.Result.State != core.Accepted || j.Result.Accepted == nil || !*j.Result.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Result.NextAction != "none" {
		t.Fatalf("accepted job: %+v", j.Result)
	}
	got := core.Acceptance{Receipt: j.Receipt, Card: j.Result.Card, Topic: j.Result.Topic, Room: j.Result.Room, Pin: j.Result.Pin, Activity: j.Result.Activity, ReplySignal: j.Result.ReplySignal, Poll: j.Result.Poll}
	data, err := got.Encode(tc.request(), j.UpstreamID, "author")
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("evidence changed: %s, want %s (%v)", data, want, err)
	}
	if r := got.MessageReceipt(); r != nil {
		if j.Result.Persisted == nil || *j.Result.Persisted != r.Receipt.Persisted || j.Result.MessageID != r.ID || !reflect.DeepEqual(j.Result.RecipientConnected, r.Receipt.RecipientConnected) {
			t.Fatalf("message facts: %+v", j.Result)
		}
	} else if j.Result.Persisted != nil || j.Result.RecipientConnected != nil || j.Result.MessageID != "" {
		t.Fatalf("fabricated message facts: %+v", j.Result)
	}
}

// TestAcceptance runs every durable operation's acceptance through the
// journal: correlation, exclusivity, save, restart, retention, save failure
// and its retry, stale attempts, and restart after a failed save.
func TestAcceptance(t *testing.T) {
	cases := acceptanceCases()
	for _, op := range []core.Operation{core.Send, core.Reply, core.ReactionAdd, core.MessageAction, core.MessageCorrect, core.MessageRetract, core.ProfileUpdate, core.PresenceSet, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.ActivitySet, core.ReplySignalSet, core.ReplySignalCancel, core.PollCreate, core.PollVote, core.PollClose} {
		tc, ok := cases[op]
		if !ok || !op.IsMutation() {
			t.Fatalf("no fixture for %s", op)
		}
		delete(cases, op)
		t.Run(string(op), func(t *testing.T) { testAcceptance(t, op, tc) })
	}
	if len(cases) != 0 {
		t.Fatalf("fixtures not run: %v", cases)
	}
}

func testAcceptance(t *testing.T, op core.Operation, tc acceptanceCase) {
	now := time.Unix(1000, 0)
	t.Run("accepted", func(t *testing.T) {
		c, identity, _ := fixture(t)
		ctx := t.Context()
		s, err := Open(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { s.Close() }()
		g, a := consumed(t, s, identity, tc.request(), "", now)
		// The consumed copy holds the admitted intent; a caller's later
		// changes did not reach it, and a backend's do not reach the store's.
		if want := tc.request(); !reflect.DeepEqual(a.Job.Request, want) {
			t.Fatalf("consumed intent %+v, want %+v", a.Job.Request, want)
		}
		scribble(&a.Job.Request)
		var lookup int
		if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs").Scan(&lookup); err != nil || (lookup != 0) != op.HasLookup() {
			t.Fatalf("lookup budget %d: %v", lookup, err)
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET outcome=x'7b7d'"); err == nil {
			t.Fatal("journal held an outcome for an unaccepted job")
		}
		for i, wrong := range tc.wrong {
			evidence := tc.evidence(a.Job.UpstreamID)
			wrong(&evidence)
			if _, err = s.Accept(ctx, g, evidence, now, now); !errors.Is(err, ErrInvalid) {
				t.Fatalf("uncorrelated evidence %d saved: %v", i, err)
			}
		}
		two := tc.evidence(a.Job.UpstreamID)
		if two.Room == nil {
			two.Room = &core.RoomAcknowledgement{}
		} else {
			two.Pin = &core.PinAcknowledgement{}
		}
		for _, evidence := range []core.Acceptance{{}, otherKind(op), two} {
			if _, err = s.Accept(ctx, g, evidence, now, now); !errors.Is(err, ErrInvalid) {
				t.Fatalf("evidence %+v saved: %v", evidence, err)
			}
		}
		if _, err = s.Accept(ctx, g, tc.evidence(a.Job.UpstreamID), a.Deadline, a.Deadline); !errors.Is(err, ErrInvalid) {
			t.Fatalf("evidence observed at the deadline saved: %v", err)
		}
		want, err := tc.evidence(a.Job.UpstreamID).Encode(tc.request(), a.Job.UpstreamID, "author")
		if err != nil {
			t.Fatal(err)
		}
		j, err := s.Accept(ctx, g, tc.evidence(a.Job.UpstreamID), now, now)
		if err != nil {
			t.Fatal(err)
		}
		checkAccepted(t, tc, j, want)
		before, _ := json.Marshal(j.Result)
		var stored []byte
		if err = s.db.QueryRowContext(ctx, "SELECT outcome FROM jobs").Scan(&stored); err != nil || !bytes.Equal(stored, want) {
			t.Fatalf("journal outcome %s: %v", stored, err)
		}
		if _, err = s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
			t.Fatalf("accepted job resent: %v", err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
		if s, err = Open(ctx, c); err != nil {
			t.Fatal(err)
		}
		j, err = s.LookupAuthor(ctx, identity, "request")
		if err != nil {
			t.Fatal(err)
		}
		checkAccepted(t, tc, j, want)
		if after, _ := json.Marshal(j.Result); !bytes.Equal(after, before) {
			t.Fatalf("result changed on reopen:\n%s\n%s", before, after)
		}
		if _, err = s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
			t.Fatalf("reopen resent accepted job: %v", err)
		}
		if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err = s.LookupAuthor(ctx, identity, "request"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("accepted job outlived retention: %v", err)
		}
	})
	t.Run("save_failure", func(t *testing.T) {
		c, identity, _ := fixture(t)
		ctx := t.Context()
		s, err := Open(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		g, a := consumed(t, s, identity, tc.request(), "", now)
		if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_save BEFORE UPDATE OF outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
			t.Fatal(err)
		}
		evidence := tc.evidence(a.Job.UpstreamID)
		if _, err = s.Accept(ctx, g, evidence, now, now); err == nil || errors.Is(err, ErrInvalid) {
			t.Fatalf("failed save: %v", err)
		}
		if _, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); !errors.Is(err, ErrNotReady) {
			t.Fatalf("completion erased observed evidence: %v", err)
		}
		if _, err = s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
			t.Fatalf("failed save permitted resend: %v", err)
		}
		if changed := tc.evidence(a.Job.UpstreamID); variant(&changed) {
			if _, err = s.Accept(ctx, g, changed, now, now); !errors.Is(err, ErrConflict) {
				t.Fatalf("other evidence replaced the observed: %v", err)
			}
		}
		// The caller's evidence object changes after the failed save.
		tc.wrong[0](&evidence)
		if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_save"); err != nil {
			t.Fatal(err)
		}
		want, _ := tc.evidence(a.Job.UpstreamID).Encode(tc.request(), a.Job.UpstreamID, "author")
		for _, at := range []time.Time{now.Add(40 * time.Second), now.Add(41 * time.Second)} {
			j, err := s.RetryAcceptance(ctx, g, at)
			if err != nil {
				t.Fatal(err)
			}
			checkAccepted(t, tc, j, want)
		}
	})
	t.Run("stale_attempt", func(t *testing.T) {
		for _, change := range []string{"attempt_id='newer-attempt'", "attempt_kind='lookup'"} {
			c, identity, _ := fixture(t)
			ctx := t.Context()
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			g, a := consumed(t, s, identity, tc.request(), "", now)
			if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET "+change); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Accept(ctx, g, tc.evidence(a.Job.UpstreamID), now, now); !errors.Is(err, ErrStaleAttempt) {
				t.Fatalf("%s: old attempt overwrote newer marker: %v", change, err)
			}
			if j, err := s.LookupAuthor(ctx, identity, "request"); err != nil || j.Result.State == core.Accepted || j.Receipt != nil {
				t.Fatalf("%s: stale acceptance persisted: %+v %v", change, j.Result, err)
			}
			s.Close()
		}
	})
	if op.HasLookup() {
		return // Restart with a lookup budget: TestAcceptanceProcessDeath.
	}
	for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
		t.Run("restart_after_save_failure/"+string(policy), func(t *testing.T) {
			c, identity, _ := fixture(t)
			ctx := t.Context()
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			g, a := consumed(t, s, identity, tc.request(), policy, now)
			if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_save BEFORE UPDATE OF outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Accept(ctx, g, tc.evidence(a.Job.UpstreamID), now, now); err == nil {
				t.Fatal("failed save claimed acceptance")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if s, err = Open(ctx, c); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			later := now.Add(time.Second)
			j, err := s.LookupAuthor(ctx, identity, "request")
			if err != nil || !j.Result.AcceptanceUncertain {
				t.Fatalf("restart lost uncertainty: %+v %v", j.Result, err)
			}
			if _, err = s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, later); !errors.Is(err, ErrNotReady) {
				t.Fatalf("spent a lookup budget: %v", err)
			}
			if policy == core.ReceiptOnly {
				if _, err = s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, later); !errors.Is(err, ErrNotReady) {
					t.Fatalf("receipt-only job resent: %v", err)
				}
				return
			}
			if !j.Result.DuplicateRisk || j.Result.NextAction != "send" {
				t.Fatalf("retry state: %+v", j.Result)
			}
			if _, err = s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, later); err != nil {
				t.Fatalf("retry policy did not permit bounded resend: %v", err)
			}
		})
	}
}

func TestObservedAcceptanceSavesAfterExpiry(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := request()
	expires := int64(1005)
	r.ExpiresAt = &expires
	g, a := consumed(t, s, identity, r, "", now)
	j, err := s.RecordAcceptance(ctx, g, receiptFor(a), time.Unix(1004, 0), time.Unix(1010, 0))
	if err != nil || j.Result.State != core.Accepted {
		t.Fatal("known acceptance discarded after expiry", err)
	}
}

func TestLargeEscapedValidReceipt(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := request()
	r.Message = strings.Repeat("<", 4096)
	g, a := consumed(t, s, identity, r, "", now)
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now); err != nil {
		t.Fatal("JSON escaping rejected a valid full-size message", err)
	}
}

func TestRetainedLookupResolvesAcceptanceAfterRestart(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("lookup allowed before any send", err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err = s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now)
	if err != nil || j.Result.State != core.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 1 {
		t.Fatal("retained lookup did not resolve prior send", j, err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("lookup acceptance allowed resend", err)
	}
}
