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
