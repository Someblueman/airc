package backend

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func registrationFailure(a store.Attempt, rejected *irc.RejectedError) Outcome {
	o := failed(a, core.AuthenticationFailed, false, false)
	if rejected.Code == "437" {
		o = failed(a, core.CapacityExceeded, true, false)
	}
	if rejected.Code == "421" {
		o = failed(a, core.UnsupportedOperation, false, false)
	}
	o.Completion.Failure.UpstreamCode, _ = strconv.Atoi(rejected.Code)
	return o
}

func exchange(ctx context.Context, client *irc.Client, a store.Attempt, replyRoot string) Outcome {
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	may := a.Kind == store.SendAttempt
	// Close interrupts even a writer blocked inside the SDK. Join the close
	// callback before moving to receipt wait; no phase goroutine outlives Run.
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	var err error
	if may {
		if a.Job.Request.Operation == core.Reply {
			err = client.ReplyWithID(a.Job.Request.ParentID, a.Job.Request.Message, a.Job.UpstreamID)
		} else {
			err = client.SendWithID(a.Job.Request.Target.Name, a.Job.Request.Message, a.Job.UpstreamID)
		}
	} else {
		err = client.RetryRequest(a.Job.UpstreamID)
	}
	stopped := stopClose()
	cancelWrite()
	if !stopped {
		<-closed
	}
	if err != nil || writeCtx.Err() == context.DeadlineExceeded {
		return failed(a, core.UpstreamUnavailable, true, may)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWait()
	for {
		select {
		case <-waitCtx.Done():
			return failed(a, core.DeliveryUncertain, true, may)
		case event, ok := <-client.Events():
			if !ok {
				return failed(a, core.DeliveryUncertain, true, may)
			}
			switch e := event.(type) {
			case *irc.SendReceiptEvent:
				if e.RequestID != a.Job.UpstreamID {
					continue
				}
				observed := time.Now()
				if !observed.Before(a.Deadline) {
					return failed(a, core.DeliveryUncertain, true, may)
				}
				if _, err := core.ReceiptBytes(e, a.Job.Request, a.Job.UpstreamID, a.Backend.Account); err != nil || replyRoot != "" && e.ThreadID != replyRoot {
					return failed(a, core.DeliveryUncertain, true, may)
				}
				return Outcome{Attempt: a, Receipt: e, ObservedAt: observed}
			case *irc.RawEvent:
				if o, handled := numericFailure(a, e); handled {
					return o
				}
			}
		}
	}
}

// One fresh session has exactly one command owner, so these post-registration
// numerics belong to that command. Unknown errors conservatively retain risk.
func numericFailure(a store.Attempt, e *irc.RawEvent) (Outcome, bool) {
	code, err := strconv.Atoi(e.Command)
	if err != nil || code < 400 || code > 599 || code == 422 {
		return Outcome{}, false
	}
	may := a.Kind == store.SendAttempt
	o := failed(a, core.DeliveryUncertain, true, may)
	o.Completion.Failure.UpstreamCode = code
	switch code {
	case 488:
		if may {
			return o, true
		}
		o = failed(a, core.DeliveryUncertain, true, false)
	case 486:
		// This exact pinned-server grammar has an explicit retry delay.
		const prefix, suffix = "Slow mode: retry after ", " seconds"
		delay := strings.TrimSuffix(strings.TrimPrefix(e.Trailing, prefix), suffix)
		seconds, err := strconv.ParseInt(delay, 10, 64)
		if err != nil || seconds < 1 || e.Trailing != prefix+delay+suffix {
			return o, true
		}
		o = failed(a, core.CapacityExceeded, true, false)
		o.Completion.RetryAfterSeconds = seconds
	case 437:
		o = failed(a, core.CapacityExceeded, true, false)
	case 404, 481, 482, 484:
		o = failed(a, core.Forbidden, false, false)
	case 401, 403, 407, 412, 417, 430, 461:
		o = failed(a, core.InvalidRequest, false, false)
	case 421:
		o = failed(a, core.UnsupportedOperation, false, false)
	case 487:
		o = failed(a, core.IDConflict, false, false)
	}
	o.Completion.Failure.UpstreamCode = code
	return o, true
}
