//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWaitingReadForeignParentDeniedEvenWithEmptySignals(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.SendWithID("#foreign-question", "foreign retained question", "foreign-question"); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "foreign-question" })
	if receipt.Receipt == nil || !receipt.Receipt.Accepted {
		t.Fatal("foreign question was not accepted", receipt)
	}
	request := core.Request{Version: 1, RequestID: "waiting-foreign-parent", Operation: core.WaitingRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: receipt.ID}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusForbidden || result.Waiting != nil {
		t.Fatalf("foreign parent disclosed: status=%d result=%+v", status, result)
	}
}
