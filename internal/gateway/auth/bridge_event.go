package auth

import (
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// BridgeEvent is a provider-verified bridged message ready for admission:
// the author derived from the provider's verified identifier, the room's
// fixed route, and the request the text asks for. Only a bridge session can
// mint one; its zero value admits nothing.
type BridgeEvent struct {
	author      BridgeAuthor
	route       bridgeRoute
	request     core.Request
	correlation string
	read        bool
	refused     bool
	until       time.Time
}

func newBridgeEvent(author BridgeAuthor, route bridgeRoute, body, requestID string, until time.Time) BridgeEvent {
	r, correlation, read, err := bridgeRequest(body, route.target, requestID)
	if err != nil {
		t := route.target
		r = core.Request{Version: 1, RequestID: requestID, Operation: core.HistoryRead, Target: &t}
	}
	return BridgeEvent{author: author, route: route, request: r, correlation: correlation, read: read, refused: err != nil, until: until}
}

func (e BridgeEvent) Valid() bool {
	return e.author.Author.Valid() && e.request.RequestID != "" && e.request.Target != nil && !e.until.IsZero()
}

// Author is who the message posts or reads as.
func (e BridgeEvent) Author() BridgeAuthor { return e.author }

// Target is the room's route target.
func (e BridgeEvent) Target() core.Target { return e.route.target }

// IsRead reports a read request (AIRC-READ/1); everything else is a send.
func (e BridgeEvent) IsRead() bool { return e.Valid() && e.read }

// Refused reports text that started a read request but was malformed. It is
// answered with invalid_request and never posted.
func (e BridgeEvent) Refused() bool { return e.Valid() && e.refused }

// Allowed reports whether the route carries the requested operation.
func (e BridgeEvent) Allowed() bool {
	return e.Valid() && !e.refused && slices.Contains(e.route.operations, e.request.Operation)
}

// CorrelationID is the optional client handle from a read request.
func (e BridgeEvent) CorrelationID() string { return e.correlation }

// RequestID is derived from the provider's message identity, so a replay of
// the same provider message is the same AIRC request.
func (e BridgeEvent) RequestID() string { return e.request.RequestID }

// ReplayDeadline ends the provider event's freshness window.
func (e BridgeEvent) ReplayDeadline() time.Time { return e.until }

// Request returns a copy of the contract request.
func (e BridgeEvent) Request() core.Request {
	r := e.request
	if r.Target != nil {
		t := *r.Target
		r.Target = &t
	}
	return r
}
