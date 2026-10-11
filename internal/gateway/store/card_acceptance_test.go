//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func cardFixture(t *testing.T) (c config.Config, identity Submission) {
	t.Helper()
	c, _, _ = fixture(t)
	identity = fixtureAuthor()
	return c, identity
}

func profileRequest() core.Request {
	model := "model"
	return core.Request{Version: 1, RequestID: "card-request", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model}}
}

func profileCard() *core.CardAcknowledgement {
	return &core.CardAcknowledgement{Version: 1, Operation: core.ProfileUpdate, Card: irc.AgentCard{Nick: "author", AccountID: "0123456789abcdef0123456789abcdef", Model: "model", State: "unknown", LastSeen: time.Unix(1000, 0).UTC()}}
}

func TestCardAcceptanceSurvivesRestartWithoutReceiptFacts(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitAuthor(ctx, identity, profileRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.CardAckBytes(profileCard(), a.Job.Request, "author"); err != nil {
		t.Fatalf("fixture card invalid: %#v %v", profileCard().Card, err)
	}
	if _, err = s.RecordCardAcceptance(ctx, g, profileCard(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("card accepted request was resent", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.LookupAuthor(ctx, identity, "card-request")
	if err != nil || j.Result.Card == nil || j.Receipt != nil || j.Result.Persisted != nil || j.Result.RecipientConnected != nil || j.Result.MessageID != "" || j.Result.Accepted == nil || !*j.Result.Accepted {
		t.Fatalf("card facts not retained cleanly: %#v %v", j, err)
	}
	_ = a
}

func TestCardAcceptanceRetriesImmutableEvidence(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, profileRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_card BEFORE UPDATE OF card_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	card := profileCard()
	if _, err = s.RecordCardAcceptance(ctx, g, card, now, now); err == nil {
		t.Fatal("failed card save claimed acceptance")
	}
	card.Card.Model = "caller mutation"
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_card"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Result.Card == nil || j.Result.Card.Card.Model != "model" {
		t.Fatalf("immutable card retry failed: %#v %v", j, err)
	}
}

func TestCardMutationUsesNoLookupBudget(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, profileRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	var limit int
	if err = s.db.QueryRowContext(ctx, "SELECT lookup_limit FROM jobs WHERE request_id='card-request'").Scan(&limit); err != nil || limit != 0 {
		t.Fatalf("card lookup budget = %d, %v", limit, err)
	}
}

func TestReceiptAndCardOutcomeAreExclusive(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AdmitAuthor(ctx, identity, profileRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE jobs SET receipt=?, card_outcome=? WHERE request_id='card-request'", []byte(`{}`), []byte(`{}`)); err == nil {
		t.Fatal("journal accepted simultaneous receipt and card evidence")
	}
}

func TestCardGrantConsumptionKeepsPrivateProfilePointers(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	req := profileRequest()
	if _, _, err = s.AdmitAuthor(ctx, identity, req, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	*a.Job.Request.Profile.Model = "caller mutation"
	if _, err = s.RecordCardAcceptance(ctx, g, profileCard(), now, now); err != nil {
		t.Fatalf("private request pointers changed: %v", err)
	}
}

func TestFailedCardSaveRestartsAsBoundedRetryWithoutLookup(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitAuthor(ctx, identity, profileRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_card BEFORE UPDATE OF card_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordCardAcceptance(ctx, g, profileCard(), now, now); err == nil {
		t.Fatal("failed card save claimed acceptance")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.LookupAuthor(ctx, identity, "card-request")
	if err != nil || !j.Result.DuplicateRisk || !j.Result.AcceptanceUncertain || j.Result.NextAction != "send" {
		t.Fatalf("restart lost bounded retry uncertainty: %#v %v", j, err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "card-request", LookupAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatalf("card mutation spent lookup budget: %v", err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now.Add(time.Second)); err != nil {
		t.Fatalf("retry policy did not permit bounded resend: %v", err)
	}
}

func TestFailedCardSaveReceiptOnlyStaysUncertain(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	receiptOnly := core.ReceiptOnly
	r := profileRequest()
	r.DeliveryPolicy = &receiptOnly
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
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fixture_card BEFORE UPDATE OF card_outcome ON jobs BEGIN SELECT RAISE(ABORT,'fixture save failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordCardAcceptance(ctx, g, profileCard(), now, now); err == nil {
		t.Fatal("failed card save claimed acceptance")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, SendAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatalf("receipt-only mutation resent: %v", err)
	}
	if _, err = s.Reserve(ctx, scopeOf(s, identity), r.RequestID, LookupAttempt, now.Add(time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatalf("receipt-only card mutation looked up: %v", err)
	}
}

func TestAcceptedCardIsPrunedByRetention(t *testing.T) {
	c, identity := cardFixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitAuthor(ctx, identity, profileRequest(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "card-request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Consume(now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordCardAcceptance(ctx, g, profileCard(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Maintain(ctx, now.Add(7*24*time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupAuthor(ctx, identity, "card-request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("accepted card was not pruned: %v", err)
	}
	s.Close()
}
