package adapter

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type amqpAdmission struct {
	requestID string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	condition string // Refusal before a request identity exists.
}

// admitAMQP runs one complete transport message's plain contract request as
// the author it names, else the connection's identified author. The request
// names its own target, as on every other carrier; the listener's policy
// bounds operations and targets, and a route's operations, when set, narrow
// them. A successful send result exists only after durable intake. Wire
// metadata is neither replay identity nor a way to redirect the fixed route.
func admitAMQP(parent context.Context, d *dispatch.Dispatcher, in dispatch.Ingress, route config.AMQPRoute, incoming *amqpIncoming, requests chan struct{}) amqpAdmission {
	if d == nil || incoming == nil || !incoming.complete || cap(requests) < 1 {
		return amqpAdmission{condition: "amqp:illegal-state"}
	}
	message, err := amqpwire.DecodeMessage(incoming.encoded)
	if err != nil {
		return amqpAdmission{condition: "amqp:invalid-field"}
	}
	now := time.Now()
	deadline, condition := amqpMetadata(message, route, incoming.deadline.Add(-amqpMessageDeadline), now)
	if condition != "" {
		return amqpAdmission{condition: condition}
	}
	sub, err := core.DecodeSubmission(message.Body)
	if err != nil {
		return amqpAdmission{condition: "amqp:decode-error"}
	}
	admission := amqpAdmission{requestID: sub.Request.RequestID}
	if admission.requestID == "" {
		admission.requestID = "-"
	}
	if !routeRequestAllowed(sub.Request, route.Operations) || !amqpRequestOperation(sub.Request.Operation) {
		admission.failure = &core.Failure{Code: core.Forbidden}
		return admission
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		admission.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return admission
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	result, err := d.SubmitDecoded(ctx, in, sub, time.Now())
	if err == nil && sub.Request.Operation == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		failure := errorFailureOf(err)
		admission.failure = &failure
	} else {
		admission.result = &result
	}
	return admission
}

// routeRequestAllowed reports whether a request fits a broker route: one of
// its operations when it lists any, or account.identify or capabilities.get,
// which any route carries. The request chooses its target.
func routeRequestAllowed(r core.Request, operations []core.Operation) bool {
	if r.Operation == core.AccountIdentify || r.Operation == core.CapabilitiesGet {
		return true
	}
	return len(operations) == 0 || slices.Contains(operations, r.Operation)
}

func amqpRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search || op == core.AccountIdentify || op == core.CapabilitiesGet
}

// Metadata can shorten intake's lifetime but cannot extend it or choose another
// request endpoint or reply destination. Expiration applies to this terminal
// AMQP intake; admitted AIRC jobs keep their own journal lifetime. user-id
// confers nothing: the author is in the request.
func amqpMetadata(message amqpwire.Message, route config.AMQPRoute, arrived, now time.Time) (time.Time, string) {
	deadline := now.Add(amqpDeadline)
	if message.UnsupportedAnnotations {
		return time.Time{}, "amqp:not-implemented"
	}
	for _, pair := range []struct {
		index    int
		expected string
	}{{2, route.RequestAddress}, {4, route.ResponseAddress}} {
		field := amqpFieldValue(message.Properties, pair.index)
		if field.Code != 0x40 && string(field.Data) != pair.expected {
			return time.Time{}, "amqp:unauthorized-access"
		}
	}
	if content := amqpFieldValue(message.Properties, 6); content.Code != 0x40 && string(content.Data) != "application/json" && string(content.Data) != "text/plain" {
		return time.Time{}, "amqp:not-implemented"
	}
	if encoding := amqpFieldValue(message.Properties, 7); encoding.Code != 0x40 {
		return time.Time{}, "amqp:not-implemented"
	}
	if ttl := amqpFieldValue(message.Header, 2); ttl.Code != 0x40 {
		deadline = amqpEarlier(deadline, arrived.Add(time.Duration(ttl.Number)*time.Millisecond))
	}
	if expiry := amqpFieldValue(message.Properties, 8); expiry.Code != 0x40 {
		deadline = amqpEarlier(deadline, time.UnixMilli(int64(expiry.Number)))
	}
	if !now.Before(deadline) {
		return time.Time{}, "amqp:precondition-failed"
	}
	return deadline, ""
}
