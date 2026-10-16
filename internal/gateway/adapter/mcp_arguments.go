package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type mcpSendArguments struct {
	RequestID      string       `json:"request_id"`
	Target         *core.Target `json:"target"`
	Message        string       `json:"message"`
	IssuedAt       *int64       `json:"issued_at,omitempty"`
	ExpiresAt      *int64       `json:"expires_at,omitempty"`
	DeliveryPolicy *core.Policy `json:"delivery_policy,omitempty"`
}

type mcpReadArguments struct {
	RequestID  string       `json:"request_id"`
	Target     *core.Target `json:"target"`
	Limit      int          `json:"limit"`
	ParentID   string       `json:"parent_id,omitempty"`
	ThreadMode string       `json:"thread_mode,omitempty"`
	Cursor     string       `json:"cursor,omitempty"`
	Start      string       `json:"start,omitempty"`
}

type mcpCheckArguments struct {
	RequestID   string           `json:"request_id"`
	Rooms       []core.CheckRoom `json:"rooms,omitempty"`
	Inbox       *core.CheckInbox `json:"inbox,omitempty"`
	Limit       int              `json:"limit"`
	WaitSeconds int              `json:"wait_seconds,omitempty"`
}

type mcpStatusArguments struct {
	RequestID string  `json:"request_id"`
	LookupID  *string `json:"lookup_id,omitempty"`
}

// Decode fixed public schemas before constructing the internal gateway request.
// Tool names alone select operations; callers cannot smuggle another operation.
func decodeMCPRequest(name string, body []byte) (core.Request, error) {
	request := core.Request{Version: 1}
	switch name {
	case "send":
		var args mcpSendArguments
		if err := jsoninput.Decode(body, &args, core.MaxRequestBytes); err != nil {
			return request, err
		}
		request.RequestID, request.Operation, request.Target, request.Message = args.RequestID, core.Send, args.Target, args.Message
		request.IssuedAt, request.ExpiresAt, request.DeliveryPolicy = args.IssuedAt, args.ExpiresAt, args.DeliveryPolicy
	case "read", "wait":
		var args mcpReadArguments
		if err := jsoninput.Decode(body, &args, core.MaxRequestBytes); err != nil {
			return request, err
		}
		if args.Target == nil || args.Target.Kind != "room" {
			return request, store.ErrInvalid
		}
		request.RequestID, request.Operation, request.Target, request.Limit = args.RequestID, core.HistoryRead, args.Target, args.Limit
		if name == "wait" {
			request.Operation = core.Watch
		}
		request.ParentID, request.ThreadMode, request.Cursor, request.Start = args.ParentID, args.ThreadMode, args.Cursor, args.Start
	case "check":
		var args mcpCheckArguments
		if err := jsoninput.Decode(body, &args, core.MaxRequestBytes); err != nil {
			return request, err
		}
		request.RequestID, request.Operation, request.Limit, request.WaitSeconds = args.RequestID, core.Check, args.Limit, args.WaitSeconds
		request.Rooms, request.Inbox = args.Rooms, args.Inbox
	case "status":
		var args mcpStatusArguments
		if err := jsoninput.Decode(body, &args, core.MaxRequestBytes); err != nil {
			return request, err
		}
		request.RequestID, request.Operation = args.RequestID, core.CapabilitiesGet
		if args.LookupID != nil {
			if *args.LookupID == "" {
				return request, store.ErrInvalid
			}
			request.Operation, request.LookupID = core.ReceiptGet, *args.LookupID
		}
	default:
		return request, store.ErrInvalid
	}
	return request, request.Normalize()
}
