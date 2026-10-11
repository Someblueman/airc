package auth

import (
	"errors"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

// BridgeReadMarker starts a read request typed as ordinary chat text in a
// bridged room, on IRC, XMPP and Matrix alike:
//
//	AIRC-READ/1 {"operation":"history.read","limit":20}
//
// The JSON object takes the contract's read fields; the target is the
// room's route target and the request ID is derived from the provider's
// message ID. A bare marker reads the latest 20 messages. Every other message is a
// send to the route target.
const BridgeReadMarker = "AIRC-READ/1"

// DefaultBridgeReadLimit is the page size when a history or search request
// names none.
const DefaultBridgeReadLimit = 20

// MaxBridgeReadBytes bounds the JSON after the marker.
const MaxBridgeReadBytes = 2048

var errBridgeRead = errors.New("invalid bridged read request")

type bridgeReadBody struct {
	CorrelationID string         `json:"correlation_id,omitempty"`
	Operation     core.Operation `json:"operation"`
	Target        *core.Target   `json:"target,omitempty"`
	Cursor        string         `json:"cursor,omitempty"`
	Limit         int            `json:"limit,omitempty"`
	Query         string         `json:"query,omitempty"`
	From          string         `json:"from,omitempty"`
	ParentID      string         `json:"parent_id,omitempty"`
	ThreadMode    string         `json:"thread_mode,omitempty"`
}

// bridgeRequest turns a verified message body into the contract request it
// asks for. intent reports a read marker; a malformed read is an error and
// never falls back to posting the text.
func bridgeRequest(body string, target core.Target, requestID string) (r core.Request, correlation string, intent bool, err error) {
	t := target
	rest, isRead := strings.CutPrefix(strings.TrimSpace(body), BridgeReadMarker)
	if !isRead || rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		r = core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: &t, Message: body}
		if r.Normalize() != nil || r.Message != body {
			return core.Request{}, "", false, errBridgeRead
		}
		return r, "", false, nil
	}
	read := bridgeReadBody{Operation: core.HistoryRead}
	if rest = strings.TrimSpace(rest); rest != "" {
		if len(rest) > MaxBridgeReadBytes || jsoninput.Decode([]byte(rest), &read, MaxBridgeReadBytes) != nil {
			return core.Request{}, "", true, errBridgeRead
		}
	}
	if !slices.Contains([]core.Operation{core.ContextRead, core.HistoryRead, core.Search}, read.Operation) || !bridgeCorrelationID(read.CorrelationID) {
		return core.Request{}, "", true, errBridgeRead
	}
	if read.Target != nil {
		named := *read.Target
		if named.Normalize() != nil || named != target {
			return core.Request{}, "", true, errBridgeRead
		}
	}
	if read.Limit == 0 && (read.Operation == core.HistoryRead || read.Operation == core.Search) {
		read.Limit = DefaultBridgeReadLimit
	}
	r = core.Request{Version: 1, RequestID: requestID, Operation: read.Operation, Target: &t,
		Cursor: read.Cursor, Limit: read.Limit, Query: read.Query, From: read.From, ParentID: read.ParentID, ThreadMode: read.ThreadMode}
	if r.Normalize() != nil {
		return core.Request{}, "", true, errBridgeRead
	}
	return r, read.CorrelationID, true, nil
}

func bridgeCorrelationID(id string) bool {
	if len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// bridgeRoute is one bridged room's fixed AIRC target and operations.
type bridgeRoute struct {
	target     core.Target
	operations []core.Operation
}

func newBridgeRoute(r config.BridgeRoute) (bridgeRoute, bool) {
	target := r.Target
	if target.Normalize() != nil || target != r.Target || len(r.Operations) == 0 {
		return bridgeRoute{}, false
	}
	return bridgeRoute{target: target, operations: slices.Clone(r.Operations)}, true
}
