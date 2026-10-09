//go:build darwin || linux

package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMessageMutationOfflineDirectPersistsWithoutConnectedClaim(t *testing.T) {
	target := core.Target{Kind: "direct", Name: "post-offline-peer"}
	f := newReactionTransportFixtureWithTargets(t, false, []core.Target{target})
	parent := ""
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-offline-"+strings.TrimPrefix(string(op), "message."), op, &target, parent)
		roomBearerPost(t, f, r, 1)
		result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
		if result.RecipientConnected == nil || *result.RecipientConnected {
			t.Fatal("offline recipient connected claim", result)
		}
		author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		proof, err := irc.RequestContext(ctx, author, result.MessageID, 1)
		cancel()
		_ = author.Close()
		if err != nil || len(proof.Messages) != 1 || proof.Messages[0].ID != result.MessageID {
			t.Fatalf("native sender verification: %+v error=%v", proof, err)
		}
		meta := proof.Messages[0]
		observed := &irc.MessageEvent{ChatMetadata: meta.ChatMetadata, ID: meta.ID, From: meta.From, Target: meta.Target, Message: meta.Message, ReplyTo: meta.ReplyTo, ThreadID: meta.ThreadID, Timestamp: meta.Timestamp}
		assertMessageMutationFacts(t, result, r, observed)
		parent = result.MessageID
	}
}

func TestMessageMutationMuteBlocksWritesAndUnmuteRestores(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, f.socket, target.Name, "post-muted-parent")
	observer := newReplySignalObserver(t, f.socket, *target)
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	moderate := func(action string) {
		if err := admin.Moderate(irc.AdminRequest{Action: action, Nick: "fixture-author", Scope: target.Name}); err != nil {
			t.Fatal(err)
		}
		if event := next(t, admin, func(e *irc.AdminEvent) bool { return e.Action == action }); !event.Changed {
			t.Fatal("mute positive control failed", event)
		}
		next(t, admin, func(e *irc.EndOfAdminEvent) bool { return true })
	}
	moderate("mute")
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		p := parent
		if op == core.MessageAction {
			p = ""
		}
		r := messageMutationRequest("post-muted-"+strings.TrimPrefix(string(op), "message."), op, target, p)
		roomBearerPost(t, f, r, 1)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden || blocked.Failure.UpstreamCode != 485 {
			t.Fatalf("native mute classification: %+v", blocked)
		}
		assertNoMessageMutation(t, observer)
	}
	moderate("unmute")
	r := messageMutationRequest("post-unmuted", core.MessageCorrect, target, parent)
	roomBearerPost(t, f, r, 1)
	result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
	assertMessageMutationFacts(t, result, r, next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID }))
}
