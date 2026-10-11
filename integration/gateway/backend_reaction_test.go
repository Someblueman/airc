package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendReactionMatchesNativeMessage(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	if err := author.SendWithID("#gateway-fixture", "shared reaction parent", "reaction-parent"); err != nil {
		t.Fatal(err)
	}
	parent := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "reaction-parent" })
	root := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == parent.ID })
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	identity := fixtureIdentity(t, c)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	request := core.Request{Version: 1, RequestID: "shared-reaction", Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Reaction: "agree"}
	grant, outcome := replyAttempt(t, journal, b, identity, request)
	if outcome.Receipt == nil {
		t.Fatalf("reaction acceptance missing: %+v", outcome)
	}
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == outcome.Receipt.ID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Reaction || seen.Reaction != request.Reaction || seen.ReplyTo != parent.ID || seen.ThreadID != parent.ID || seen.AccountID == "" || seen.AccountID != root.AccountID {
		t.Fatalf("shared backend reaction differs from native parent/intent: %+v root=%+v", seen, root)
	}
	job, err := journal.RecordAcceptance(t.Context(), grant, outcome.Receipt, outcome.ObservedAt, time.Now())
	if err != nil || job.Result.State != core.Accepted || job.Result.MessageID != seen.ID || job.Result.SendAttempts != 1 {
		t.Fatal("reaction facts were not saved", job, err)
	}
}
