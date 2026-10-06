//go:build darwin || linux

package gateway_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendMessageMutationsMatchNativeHistory(t *testing.T) {
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, socket, *target)
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.MessageAction, core.MessageCorrect, core.MessageRetract)
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
	var action, correction, retraction *irc.MessageEvent
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		parent := ""
		if action != nil {
			parent = action.ID
		}
		request := messageMutationRequest("shared-"+strings.ReplaceAll(string(op), ".", "-"), op, target, parent)
		grant, outcome := replyAttempt(t, journal, b, identity, request)
		if outcome.Receipt == nil {
			t.Fatalf("native mutation acceptance missing: %+v", outcome)
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == outcome.Receipt.ID })
		job, err := journal.RecordAcceptance(t.Context(), grant, outcome.Receipt, outcome.ObservedAt, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		assertMessageMutationFacts(t, job.Result, request, seen)
		if action != nil && (seen.ThreadID != action.ID || seen.AccountID != action.AccountID) {
			t.Fatalf("mutation changed root or account: %+v action=%+v", seen, action)
		}
		switch op {
		case core.MessageAction:
			action = seen
		case core.MessageCorrect:
			correction = seen
		case core.MessageRetract:
			retraction = seen
		}
	}
	var foundAction, foundCorrection bool
	for _, message := range nativeWebTransportMessages(t, socket, target.Name) {
		if message.ID == action.ID && message.Retracted && message.SupersededBy == retraction.ID {
			foundAction = true
		}
		if message.ID == correction.ID && message.Message == correction.Message && message.Supersedes == action.ID && message.ThreadID == action.ID {
			foundCorrection = true
		}
	}
	if !foundAction || !foundCorrection {
		t.Fatal("native history lost action/correction/retraction annotations")
	}
}
