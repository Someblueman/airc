//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestMessageMutationHTTP maps message.action, message.correct and
// message.retract to native writes: each saved receipt names the message the
// room observes, gateway history returns it unchanged, and history carries the
// native correction and retraction annotations on the original.
func TestMessageMutationHTTP(t *testing.T) {
	f := newReactionHTTPFixture(t)
	target := contractRoom()
	observer := newReplySignalObserver(t, f.socket, *target)
	var action *irc.MessageEvent
	ids := map[core.Operation]string{}
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		parent := ""
		if action != nil {
			parent = action.ID
		}
		r := messageMutationRequest("message-http-"+mutationKind(op), op, target, parent)
		if op == core.MessageRetract {
			if status, admission := reactionHTTPCall(t, f, f.cred.token(t, r, true)); status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil {
				t.Fatalf("request-route mutation admission: %d %+v", status, admission)
			}
		} else {
			headerPost(t, f, r)
		}
		result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
		message := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		assertMessageMutationFacts(t, result, r, message)
		compareMessageHistory(t, f, r, message)
		if action != nil && (message.ThreadID != action.ID || message.AccountID != action.AccountID) {
			t.Fatalf("mutation changed root or account: %+v action=%+v", message, action)
		}
		if op == core.MessageAction {
			action = message
		}
		ids[op] = result.MessageID
	}
	history := core.Request{Version: 1, RequestID: "message-http-annotations", Target: target}
	if original := reactionHistory(t, f, history, action.ID); !original.Retracted || original.SupersededBy != ids[core.MessageRetract] {
		t.Fatalf("history lost the retraction annotation: %+v", original)
	}
	if correction := reactionHistory(t, f, history, ids[core.MessageCorrect]); correction.Supersedes != action.ID || correction.ThreadID != action.ID {
		t.Fatalf("history lost the correction annotation: %+v", correction)
	}
}
