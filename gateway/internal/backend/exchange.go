package backend

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func cardExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	var err error
	request := a.Job.Request
	if request.Operation == core.ProfileUpdate {
		fields := map[string]string{}
		if request.Profile.Clear {
			fields["clear"] = "true"
		} else {
			if request.Profile.Model != nil {
				fields["model"] = *request.Profile.Model
			}
			if request.Profile.Workspace != nil {
				fields["workspace"] = *request.Profile.Workspace
			}
			if request.Profile.Tools != nil {
				fields["tools"] = *request.Profile.Tools
			}
			if request.Profile.About != nil {
				fields["about"] = *request.Profile.About
			}
		}
		err = client.UpdateProfile(fields)
	} else {
		seconds := time.Duration(request.Presence.TTLSeconds) * time.Second
		err = client.SetPresence(request.Presence.State, request.Presence.Note, seconds)
	}
	stopped := stopClose()
	cancelWrite()
	if !stopped {
		<-closed
	}
	if err != nil || writeCtx.Err() == context.DeadlineExceeded {
		return failed(a, core.UpstreamUnavailable, true, a.Kind == store.SendAttempt)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWait()
	var card *irc.AgentCard
	for {
		select {
		case <-waitCtx.Done():
			return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
		case event, ok := <-client.Events():
			if !ok {
				return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
			}
			switch e := event.(type) {
			case *irc.DirectoryEvent:
				if card != nil || e.Nick != client.Nick() {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				copy := e.AgentCard
				card = &copy
			case *irc.EndOfDirectoryEvent:
				if card == nil {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				return accepted(waitCtx, a, core.Acceptance{Card: &core.CardAcknowledgement{Version: 1, Operation: request.Operation, Card: *card}}, client.Nick())
			case *irc.RawEvent:
				if o, handled := numericFailure(a, e); handled {
					return o
				}
			}
		}
	}
}

// registrationFailure maps AIRC's refusal of the job's author. A guest refused
// with 498 chose a registered nickname: a permanent nick_registered, distinct
// from a registered author's rejected token (498 or SASL 904), which is
// authentication_failed. Neither is retried with the same author.
func registrationFailure(a store.Attempt, rejected *irc.RejectedError) Outcome {
	o := failed(a, core.AuthenticationFailed, false, false)
	if rejected.Code == "498" && !a.Job.Author.Registered {
		o = failed(a, core.NickRegistered, false, false)
	}
	if rejected.Code == "437" {
		o = failed(a, core.CapacityExceeded, true, false)
	}
	if rejected.Code == "421" {
		o = failed(a, core.UnsupportedOperation, false, false)
	}
	if rejected.Throttled {
		// AIRC did not check the credential; the same author may succeed
		// once the source's failure window passes.
		o = failed(a, core.CapacityExceeded, true, false)
	}
	o.Completion.Failure.UpstreamCode, _ = strconv.Atoi(rejected.Code)
	return o
}

func sendExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	return exchange(ctx, client, a, "")
}

// parentExchange writes a reply or reaction only under a parent AIRC shows
// the author's session; the receipt must then land in the parent's thread.
func parentExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	root, rejection := authorizeParent(ctx, client, a)
	if rejection != nil {
		return *rejection
	}
	return exchange(ctx, client, a, root)
}

func reactionExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	if !builtinReaction(a.Job.Request.Reaction) && client.Features()["CUSTOM_REACTIONS"] != "1" {
		return failed(a, core.UnsupportedOperation, false, false)
	}
	return parentExchange(ctx, client, a)
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
		if a.Job.Request.Operation == core.ReactionAdd {
			err = client.React(a.Job.Request.ParentID, a.Job.Request.Reaction)
		} else if a.Job.Request.Operation == core.Reply {
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
				if a.Job.Request.Operation != core.ReactionAdd && e.RequestID != a.Job.UpstreamID {
					continue
				}
				if replyRoot != "" && e.ThreadID != replyRoot {
					return failed(a, core.DeliveryUncertain, true, may)
				}
				return accepted(waitCtx, a, core.Acceptance{Receipt: e}, a.Job.Author.Nick)
			case *irc.RawEvent:
				if o, handled := numericFailure(a, e); handled {
					return o
				}
			}
		}
	}
}

func builtinReaction(kind string) bool {
	return kind == "seen" || kind == "checking" || kind == "agree" || kind == "disagree"
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
	case 404, 481, 482, 484, 485:
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
