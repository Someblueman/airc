package adapter

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type amqpAdmission struct {
	requestID string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	condition string // Refusal before authenticating an authorized response scope.
}

// admitAMQP converts one complete transport message to signed gateway intent.
// A successful send result exists only after durable intake. Wire metadata is
// neither replay identity nor a way to redirect or broaden the fixed grant.
func admitAMQP(parent context.Context, d *dispatch.Dispatcher, identity auth.Identity, route config.AMQPRoute, in *amqpIncoming, requests chan struct{}) amqpAdmission {
	if d == nil || in == nil || !in.complete || cap(requests) < 1 {
		return amqpAdmission{condition: "amqp:illegal-state"}
	}
	message, err := amqpwire.DecodeMessage(in.encoded)
	if err != nil {
		return amqpAdmission{condition: "amqp:invalid-field"}
	}
	now := time.Now()
	deadline, condition := amqpMetadata(message, identity.ID(), route, in.deadline.Add(-amqpMessageDeadline), now)
	if condition != "" {
		return amqpAdmission{condition: condition}
	}
	verified, err := d.AuthenticateDetached(string(message.Body), false, now)
	if err != nil || verified.PrincipalID() != identity.ID() || verified.Binding() != identity.Binding() || route.Principal != identity.ID() {
		return amqpAdmission{condition: "amqp:unauthorized-access"}
	}
	target, ok := verified.Target()
	if !ok || target != route.Target || !slices.Contains(route.Operations, verified.Operation()) || !amqpRequestOperation(verified.Operation()) {
		return amqpAdmission{condition: "amqp:unauthorized-access"}
	}
	admission := amqpAdmission{requestID: verified.RequestID()}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		admission.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return admission
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		status, code := httpError(err)
		admission.failure = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		admission.result = &result
	}
	return admission
}

func amqpRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

// Metadata can shorten intake's lifetime but cannot extend signed validity or
// choose another publisher, request endpoint or reply destination. Expiration
// applies to this terminal AMQP intake; admitted AIRC jobs retain signed intent.
func amqpMetadata(message amqpwire.Message, principal string, route config.AMQPRoute, arrived, now time.Time) (time.Time, string) {
	deadline := now.Add(amqpDeadline)
	if message.UnsupportedAnnotations {
		return time.Time{}, "amqp:not-implemented"
	}
	for _, pair := range []struct {
		index    int
		expected string
	}{{1, principal}, {2, route.RequestAddress}, {4, route.ResponseAddress}} {
		field := amqpFieldValue(message.Properties, pair.index)
		if field.Code != 0x40 && string(field.Data) != pair.expected {
			return time.Time{}, "amqp:unauthorized-access"
		}
	}
	if content := amqpFieldValue(message.Properties, 6); content.Code != 0x40 && string(content.Data) != "application/jose" {
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
