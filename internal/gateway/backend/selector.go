package backend

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	threadModeThread  = "thread"
	threadModeReplies = "replies"
)

func prepareSelector(ctx context.Context, client *irc.Client, r core.Request) (core.Request, error) {
	if r.ThreadMode == "" {
		return r, nil
	}
	if r.Target == nil || r.Target.Kind != "room" || r.ParentID == "" || r.ThreadMode != threadModeThread && r.ThreadMode != threadModeReplies {
		return core.Request{}, store.ErrInvalid
	}
	features := client.Features()
	if r.ThreadMode == threadModeThread {
		if features["CHAT"] != "1" || features["CONTEXT"] != "1" || features["CONTEXT_BYTES"] != "1" {
			return core.Request{}, ErrReadUnsupported
		}
		proof, err := boundedContextRequest(ctx, client, r.ParentID, 1, core.MaxContextBytes, "room", nil)
		if err != nil {
			return core.Request{}, err
		}
		root, valid := validateThreadSelectorProof(r, proof)
		if !valid {
			return core.Request{}, auth.ErrForbidden
		}
		r.ParentID = root
		return r, nil
	}
	if features["CHECK"] == "1" {
		proofRequest := r
		proofRequest.Limit = 1
		proofRequest.Cursor = "*"
		entries, err := boundedHistoryCheck(ctx, client, proofRequest, nil)
		if err != nil {
			return core.Request{}, err
		}
		page, err := historyPage(entries, proofRequest)
		if err != nil {
			return core.Request{}, err
		}
		if len(page.Messages) != 0 {
			return r, nil
		}
	}
	if features["CHAT"] != "1" || features["CONTEXT"] != "1" || features["CONTEXT_BYTES"] != "1" {
		return core.Request{}, ErrReadUnsupported
	}
	proof, err := boundedContextRequest(ctx, client, r.ParentID, 1, core.MaxContextBytes, "room", nil)
	if err != nil {
		return core.Request{}, err
	}
	if _, err := validateContextProof(r, proof); err != nil {
		return core.Request{}, auth.ErrForbidden
	}
	return r, nil
}

func validateThreadSelectorProof(r core.Request, c irc.ConversationContext) (string, bool) {
	if root, err := validateContextProof(r, c); err == nil {
		return root, true
	}
	if r.Target == nil || !validContextSummary(c) || c.TriggerID != r.ParentID || c.RootID != r.ParentID || len(c.Messages) != 1 || !containsString(c.Missing, r.ParentID) {
		return "", false
	}
	message := c.Messages[0]
	if message.ID == r.ParentID || message.ThreadID != r.ParentID || !validContextMessage(message, r.Target.Name) {
		return "", false
	}
	if err := validateContextExtras(c, map[string]irc.MessageMetadata{message.ID: message}, r.Target.Name); err != nil {
		return "", false
	}
	return r.ParentID, true
}

func nativeSelector(r core.Request) string {
	switch r.ThreadMode {
	case threadModeThread:
		return "thread:" + r.ParentID
	case threadModeReplies:
		return "replies:" + r.ParentID
	default:
		return r.Target.Name
	}
}

func selectorMessageAllowed(message *irc.MessageMetadata, r core.Request) bool {
	if message == nil || r.Target == nil || message.Target != r.Target.Name {
		return false
	}
	switch r.ThreadMode {
	case threadModeThread:
		return message.ID == r.ParentID && message.ThreadID == "" || message.ID != r.ParentID && message.ThreadID == r.ParentID
	case threadModeReplies:
		return message.ReplyTo == r.ParentID && message.Reaction == ""
	default:
		return true
	}
}
