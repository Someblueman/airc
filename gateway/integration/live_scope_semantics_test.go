//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestEventsWatchNativeConversationSignalsProveSelectedParent(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"thread", "replies"} {
		t.Run(mode, func(t *testing.T) {
			f := newReactionTransportFixture(t)
			author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
			root := liveSemanticMessage(t, author, "", "live-scope-root")
			reply := liveSemanticMessage(t, author, root.ID, "live-scope-reply")
			nested := liveSemanticMessage(t, author, reply.ID, "live-scope-nested")
			foreign := liveSemanticMessage(t, author, "", "live-scope-other")
			native := liveSignalObserver(t, f.socket)
			parent, selected, excluded, recent := root.ID, root.ID, nested.ID, reply.ID
			if mode == "thread" {
				// External reply selector canonicalizes to the root internally.
				// Recent limit 1 deliberately omits the selected signal's parent,
				// requiring an independent context proof during the live lease.
				parent, selected, excluded, recent = reply.ID, reply.ID, foreign.ID, nested.ID
			}
			r := core.Request{Version: 1, RequestID: "live-scope-" + mode, Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1, ThreadMode: mode, ParentID: parent}
			response, reader := liveDetachedStream(t, f, r, true)
			if response.StatusCode != http.StatusOK {
				t.Fatal(response.Status)
			}
			initial, checkpoint := ssePage(t, reader)
			if len(initial.Messages) != 1 || initial.Messages[0].ID != recent || checkpoint == "" || checkpoint != initial.Cursor || initial.Status != "ok" {
				t.Fatalf("wrong conversation snapshot: %+v", initial)
			}
			liveSemanticChat(t, author, irc.ChatRequest{Action: "prepare", ID: excluded, Seconds: 20, Text: "excluded"}, "prepare")
			if observed := nextRawLiveSignal(t, native, "prepare"); observed.ID != excluded {
				t.Fatal(observed)
			}
			liveSemanticChat(t, author, irc.ChatRequest{Action: "prepare", ID: selected, Seconds: 20, Text: "selected"}, "prepare")
			if observed := liveSemanticSignal(t, native, reader, "prepare", checkpoint); observed.ID != selected || observed.Text != "selected" {
				t.Fatal(observed)
			}
			liveSemanticChat(t, author, irc.ChatRequest{Action: "cancel", ID: selected}, "cancel")
			if observed := liveSemanticSignal(t, native, reader, "cancel", checkpoint); observed.ID != selected {
				t.Fatal(observed)
			}
			// Retained positions remain scoped to the external selector and op.
			query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}, "thread_mode": {mode}, "parent_id": {foreign.ID}, "cursor": {checkpoint}}
			status, _ := topicHeaderGET(t, f, "/v1/live?"+query.Encode())
			if status != http.StatusBadRequest {
				t.Fatal("foreign conversation cursor accepted", status)
			}
			query.Set("parent_id", parent)
			status, _ = topicHeaderGET(t, f, "/v1/watch?"+query.Encode())
			if status != http.StatusBadRequest {
				t.Fatal("cross-operation cursor accepted", status)
			}
		})
	}
}
