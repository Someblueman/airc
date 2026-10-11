package backend

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// authorizeParent is a read on the same exclusive lease, before any reply write.
// Limit 1 prioritizes the exact triggering parent; MaxBytes bounds pins/profiles
// as well as messages. No retrieved body is returned to the external caller.
func authorizeParent(ctx context.Context, client *irc.Client, a store.Attempt) (string, *Outcome) {
	parentCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	closed := make(chan struct{})
	stop := context.AfterFunc(parentCtx, func() { _ = client.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	conversation, err := irc.RequestContextWithOptions(parentCtx, client, a.Job.Request.ParentID, irc.ContextOptions{Limit: 1, MaxBytes: 64 << 10}, nil)
	deadline, _ := parentCtx.Deadline()
	if err != nil || parentCtx.Err() != nil || !time.Now().Before(deadline) {
		o := failed(a, core.UpstreamUnavailable, true, false)
		var rejected *irc.RejectedError
		if errors.As(err, &rejected) {
			if classified, ok := numericFailure(a, &irc.RawEvent{Command: rejected.Code, Trailing: rejected.Message}); ok {
				o = classified
				o.Completion.MayHavePosted = false
			}
		}
		return "", &o
	}
	root, allowed := parentConversation(a, conversation)
	if !allowed {
		o := failed(a, core.Forbidden, false, false)
		return "", &o
	}
	return root, nil
}

func parentConversation(a store.Attempt, c irc.ConversationContext) (string, bool) {
	if c.TriggerID != a.Job.Request.ParentID || len(c.Messages) != 1 || c.Messages[0].ID != a.Job.Request.ParentID || len(c.RootID) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(c.RootID); err != nil {
		return "", false
	}
	for _, id := range c.Missing {
		if id == a.Job.Request.ParentID {
			return "", false
		}
	}
	parent := c.Messages[0]
	root := parent.ThreadID
	if root == "" {
		root = parent.ID
	}
	if root != c.RootID {
		return "", false
	}
	from := core.Target{Kind: "direct", Name: parent.From}
	if err := from.Normalize(); err != nil {
		return "", false
	}
	target := core.Target{Kind: a.Job.Request.Target.Kind, Name: parent.Target}
	if err := target.Normalize(); err != nil {
		return "", false
	}
	if target.Kind == "room" {
		return root, target == *a.Job.Request.Target
	}
	author := core.Target{Kind: "direct", Name: a.Job.Author.Nick}
	if err := author.Normalize(); err != nil {
		return "", false
	}
	peer := *a.Job.Request.Target
	return root, from == author && target == peer || from == peer && target == author
}
