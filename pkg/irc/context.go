package irc

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
)

type ContextSummary = protocol.ContextSummary

type ConversationContext struct {
	ContextSummary
	Messages     []MessageMetadata `json:"messages"`
	Pins         []MessageMetadata `json:"pins"`
	Participants []AgentCard       `json:"participants"`
}

// RequestContext reads original retained text and correction links. It follows
// RequestChat's rule of one outstanding request per connection and never moves
// local inbox cursors. Missing IDs and omission counts must be shown to callers.
func RequestContext(ctx context.Context, c *Client, id string, limit int) (ConversationContext, error) {
	return RequestContextWithEvents(ctx, c, id, limit, nil)
}

// RequestContextWithEvents forwards unrelated live events while retrieving context.
// Like RequestChat, it requires exclusive ownership of the event stream.
func RequestContextWithEvents(ctx context.Context, c *Client, id string, limit int, other func(Event)) (ConversationContext, error) {
	result := ConversationContext{}
	if !c.Supports("CONTEXT") {
		return result, errors.New("context requires CONTEXT on the daemon")
	}
	if !protocol.ValidMessageID(id) || limit < 1 || limit > 1000 {
		return result, errors.New("context requires a message ID and limit 1-1000")
	}
	entries, err := RequestChat(ctx, c, ChatRequest{Action: "context", ID: id, Limit: limit}, other)
	if err != nil {
		return result, err
	}
	summary := false
	for _, e := range entries {
		switch e.Action {
		case "context":
			if e.Context != nil {
				result.ContextSummary = *e.Context
				summary = true
			}
		case "context-message":
			if e.Message != nil {
				result.Messages = append(result.Messages, *e.Message)
			}
		case "context-pin":
			if e.Message != nil {
				result.Pins = append(result.Pins, *e.Message)
			}
		case "context-profile":
			if e.Profile != nil {
				result.Participants = append(result.Participants, *e.Profile)
			}
		}
	}
	if !summary {
		return result, errors.New("server did not return a context summary")
	}
	return result, nil
}
