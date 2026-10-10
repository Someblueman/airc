package adapter

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/sqswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeSQS owns one serial consumer of an explicitly provisioned Standard queue.
// Polling connections are bounded by the configured owner count (at most 16),
// independent of inbound sessions. Authentication, dispatch and result delivery
// hold shared request capacity; an idle long poll cannot occupy that capacity.
// The service closes the client and clears credentials after this function joins.
func ServeSQS(ctx context.Context, b config.SQS, client *sqswire.Client, realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, requests chan struct{}) error {
	if client == nil || d == nil || sealer == nil || recipient == nil || realm == "" || cap(requests) < 1 {
		return errors.New("invalid SQS owner")
	}
	b.Route.Operations = slices.Clone(b.Route.Operations)
	backoff := 250 * time.Millisecond
	ready := false
	for ctx.Err() == nil {
		var err error
		if !ready {
			check, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = client.CheckQueue(check, b.RequestQueueURL, true)
			if err == nil {
				err = client.CheckQueue(check, b.Route.ResultQueueURL, false)
			}
			cancel()
			ready = err == nil
		}
		if err == nil {
			poll, cancel := context.WithTimeout(ctx, 15*time.Second)
			var message *sqswire.Message
			message, err = client.Receive(poll, b.RequestQueueURL)
			cancel()
			if err == nil && message != nil {
				err = completeSQSMessage(ctx, b, client, realm, d, sealer, recipient, *message, requests)
			}
		}
		if err == nil {
			backoff = 250 * time.Millisecond
		} else {
			ready = false
			backoff = min(backoff*2, 10*time.Second)
		}
		// Also yield after immediate empty or refused messages. Queue visibility
		// and its required redrive policy own poisoned-entry retry, not a hot loop.
		if !sqsWait(ctx, backoff) {
			break
		}
	}
	return nil
}

func completeSQSMessage(parent context.Context, b config.SQS, client *sqswire.Client, realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, message sqswire.Message, requests chan struct{}) error {
	if len(message.Body) == 0 || len(message.Body) > MaxRequestFrameBytes {
		return nil
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return nil // Leave the exact receipt unacknowledged for later delivery.
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	a := admitSQS(ctx, d, b.Route, message.Body)
	if a.refused {
		return nil
	}
	payload, err := sqsResultMessage(sealer, recipient, realm, b, a, time.Now())
	completed := a.result != nil
	if errors.Is(err, auth.ErrResponseCapacity) {
		completed = false
		a.result = nil
		a.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		payload, err = sqsResultMessage(sealer, recipient, realm, b, a, time.Now())
	}
	if err != nil {
		return err
	}
	if err = client.Send(ctx, b.Route.ResultQueueURL, payload); err != nil {
		return err
	}
	// A whole read result or durable Send admission must be published before
	// deletion. Failure responses do not consume the original request.
	if completed {
		return client.Delete(ctx, b.RequestQueueURL, message.ReceiptHandle)
	}
	return nil
}

type sqsAdmission struct {
	requestID string
	binding   string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	refused   bool
}

func admitSQS(parent context.Context, d *dispatch.Dispatcher, route config.SQSRoute, token string) sqsAdmission {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err != nil {
		// Lookup-only recovery never makes expired intent executable. The store
		// compares the original fingerprint and current durable authorization.
		if slices.Contains(route.Operations, core.Send) {
			result, binding, e := d.RetainedEncryptedSend(ctx, token, route.Principal, route.Target, time.Now())
			if e == nil && result.Result != nil && result.Result.Durable {
				return sqsAdmission{requestID: result.Result.RequestID, binding: binding, result: &result}
			}
		}
		return sqsAdmission{refused: true}
	}
	target, ok := verified.Target()
	if !ok || target != route.Target || verified.PrincipalID() != route.Principal || !slices.Contains(route.Operations, verified.Operation()) || !sqsOperation(verified.Operation()) {
		return sqsAdmission{refused: true}
	}
	a := sqsAdmission{requestID: verified.RequestID(), binding: verified.Binding()}
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		status, code := httpError(err)
		a.failure = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		a.result = &result
	}
	return a
}

func sqsOperation(op core.Operation) bool {
	return op == core.Send || op == core.HistoryRead || op == core.Search || op == core.ContextRead
}

func sqsWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
