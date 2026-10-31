//go:build darwin || linux

package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMessageMutationOfflineDirectPersistsWithoutConnectedClaim(t *testing.T) {
	t.Parallel()
	target := core.Target{Kind: "direct", Name: "post-offline-peer"}
	f := newReactionFixture(t, false, []core.Target{target}, true)
	parent := ""
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-offline-"+strings.TrimPrefix(string(op), "message."), op, &target, parent)
		headerPost(t, f, r)
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

// A direct message to a one-shot agent at work says so through the gateway,
// though the agent holds no connection.
func TestMessageMutationDirectToActiveAgentSaysActive(t *testing.T) {
	t.Parallel()
	target := core.Target{Kind: "direct", Name: "post-active-peer"}
	f := newReactionFixture(t, false, []core.Target{target}, true)
	peer, err := irc.Dial(irc.Config{Nick: "post-active-peer", Network: "unix", Addr: f.socket, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := irc.RequestCheck(ctx, peer, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#work", Limit: 1}}, MaxMessages: 1}, nil); err != nil {
		t.Fatal(err)
	}
	_ = peer.Close()
	r := messageMutationRequest("post-active-action", core.MessageAction, &target, "")
	headerPost(t, f, r)
	result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
	if result.RecipientConnected == nil || *result.RecipientConnected || result.RecipientActive == nil || !*result.RecipientActive {
		t.Fatalf("an active agent's receipt: %+v", result)
	}
}
