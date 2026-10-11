package adapter

import (
	"context"
	"mime"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type stompAdmission struct {
	requestID string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	refusal   string // No request identity exists yet.
}

// admitSTOMP runs one SEND's plain contract request as the author it names,
// else the connection's identified author. The request names its own
// target; the listener's policy bounds operations and targets, and a route's
// operations, when set, narrow them. Receipt IDs and reply headers cannot
// redirect the route or replace durable replay identity. The caller may
// acknowledge a send only after this returns a durable result.
func admitSTOMP(parent context.Context, d *dispatch.Dispatcher, in dispatch.Ingress, route config.STOMPRoute, f stompwire.Frame, requests chan struct{}) stompAdmission {
	if d == nil || cap(requests) < 1 {
		return stompAdmission{refusal: "invalid STOMP admission state"}
	}
	if refusal := stompRequestMetadata(f, route); refusal != "" {
		return stompAdmission{refusal: refusal}
	}
	sub, err := core.DecodeSubmission(f.Body)
	if err != nil {
		return stompAdmission{refusal: "invalid STOMP request body"}
	}
	admission := stompAdmission{requestID: sub.Request.RequestID}
	if !routeRequestAllowed(sub.Request, route.Operations) || !stompRequestOperation(sub.Request.Operation) {
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
	ctx, cancel := context.WithTimeout(parent, stompDeadline)
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

func stompRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search || op == core.AccountIdentify || op == core.CapabilitiesGet
}

// stompRequestMetadata checks a SEND's frame. Unknown headers are ignored, as
// STOMP allows; transactions are outside this profile.
func stompRequestMetadata(f stompwire.Frame, route config.STOMPRoute) string {
	if f.Command != "SEND" || len(f.Body) == 0 || len(f.Body) > stompwire.MaxBody || len(f.Headers) > stompwire.MaxHeaders {
		return "invalid STOMP request"
	}
	if _, ok := f.Header("transaction"); ok {
		return "STOMP transactions are not supported"
	}
	destination, _ := f.Header("destination")
	if destination != route.RequestDestination {
		return "unknown STOMP destination"
	}
	if value, ok := f.Header("reply-to"); ok && value != route.ResponseDestination {
		return "STOMP reply-to must be the route's response destination"
	}
	if value, ok := f.Header("content-type"); ok {
		media, _, err := mime.ParseMediaType(value)
		if err != nil || media != "application/json" && media != "text/plain" {
			return "unsupported STOMP content type"
		}
	}
	if value, ok := f.Header("receipt"); ok && (len(value) == 0 || len(value) > 255) {
		return "invalid STOMP receipt"
	}
	return ""
}
