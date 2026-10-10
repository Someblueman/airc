package adapter

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
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
	refusal   string // No authenticated authorized response scope exists yet.
}

// admitSTOMP accepts only complete signed intent under the selected grant.
// Receipt IDs and publisher/reply headers cannot broaden authority or replace
// durable replay identity. The caller may acknowledge a send only after this
// function returns a durable result.
func admitSTOMP(parent context.Context, d *dispatch.Dispatcher, identity auth.Identity, route config.STOMPRoute, f stompwire.Frame, requests chan struct{}) stompAdmission {
	if d == nil || cap(requests) < 1 {
		return stompAdmission{refusal: "invalid STOMP admission state"}
	}
	if refusal := stompRequestMetadata(f, identity.ID(), route); refusal != "" {
		return stompAdmission{refusal: refusal}
	}
	verified, err := d.AuthenticateDetached(string(f.Body), false, time.Now())
	if err != nil || verified.PrincipalID() != identity.ID() || verified.Binding() != identity.Binding() || route.Principal != identity.ID() {
		return stompAdmission{refusal: "unauthorized STOMP request"}
	}
	target, ok := verified.Target()
	if !ok || target != route.Target || !slices.Contains(route.Operations, verified.Operation()) || !stompRequestOperation(verified.Operation()) {
		return stompAdmission{refusal: "unauthorized STOMP request"}
	}
	admission := stompAdmission{requestID: verified.RequestID()}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		admission.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return admission
	}
	ctx, cancel := context.WithTimeout(parent, stompDeadline)
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

func stompRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

func stompRequestMetadata(f stompwire.Frame, principal string, route config.STOMPRoute) string {
	if f.Command != "SEND" || len(f.Body) == 0 || len(f.Body) > stompwire.MaxBody || len(f.Headers) > stompwire.MaxHeaders {
		return "invalid STOMP request"
	}
	for _, h := range f.Headers {
		switch h.Key {
		case "destination", "content-type", "content-length", "receipt", "reply-to", "user-id":
		default:
			return "unsupported STOMP request header"
		}
	}
	destination, _ := f.Header("destination")
	if destination != route.RequestDestination {
		return "unauthorized STOMP destination"
	}
	for _, field := range []struct{ key, expected string }{{"user-id", principal}, {"reply-to", route.ResponseDestination}} {
		if value, ok := f.Header(field.key); ok && value != field.expected {
			return "unauthorized STOMP request metadata"
		}
	}
	if value, ok := f.Header("content-type"); ok && value != "application/jose" {
		return "unsupported STOMP content type"
	}
	if value, ok := f.Header("receipt"); ok && (len(value) == 0 || len(value) > 255) {
		return "invalid STOMP receipt"
	}
	return ""
}
