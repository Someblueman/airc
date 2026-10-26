package backend

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func pinExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if a.Kind != store.SendAttempt || request.Operation != core.PinsAdd && request.Operation != core.PinsRemove || request.Target == nil || request.Target.Kind != "room" || !validHistoryID(request.ParentID) {
		return failed(a, core.Forbidden, false, false)
	}
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	// Proof is a bounded phase of its own.  If it stalls, close and join the
	// session before returning; a timed-out proof must never reach the writer.
	preflightCtx, cancelPreflight := context.WithTimeout(ctx, 5*time.Second)
	closedPreflight := make(chan struct{})
	stopPreflight := context.AfterFunc(preflightCtx, func() { _ = client.Close(); close(closedPreflight) })
	preflightFinished := false
	finishPreflight := func() {
		if preflightFinished {
			return
		}
		preflightFinished = true
		stopped := stopPreflight()
		cancelPreflight()
		if !stopped {
			<-closedPreflight
		}
	}
	defer finishPreflight()
	proofRequest := core.Request{Version: 1, RequestID: request.RequestID, Operation: core.ContextRead, Target: request.Target, ParentID: request.ParentID}
	preflightFailure := func(err error) Outcome {
		if errors.Is(err, auth.ErrForbidden) {
			return failed(a, core.Forbidden, false, false)
		}
		if errors.Is(err, store.ErrCapacity) {
			return failed(a, core.CapacityExceeded, true, false)
		}
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	if request.Operation == core.PinsAdd {
		proof, err := boundedContextRequest(preflightCtx, client, request.ParentID, 1, core.MaxContextBytes, "room", nil)
		if err != nil {
			return preflightFailure(err)
		}
		if _, err := validateContextProof(proofRequest, proof); err != nil {
			return preflightFailure(err)
		}
	} else {
		pins, err := boundedPinsRequest(preflightCtx, client, request.Target.Name)
		if err != nil {
			return preflightFailure(err)
		}
		found := false
		for _, pin := range pins {
			if pin.ID == request.ParentID {
				found = true
				break
			}
		}
		if !found {
			proof, proofErr := boundedContextRequest(preflightCtx, client, request.ParentID, 1, core.MaxContextBytes, "room", nil)
			if proofErr != nil {
				return preflightFailure(proofErr)
			}
			if _, proofErr = validateContextProof(proofRequest, proof); proofErr != nil {
				return preflightFailure(proofErr)
			}
		}
	}
	preflightDeadline, _ := preflightCtx.Deadline()
	if preflightCtx.Err() != nil || !time.Now().Before(preflightDeadline) {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	finishPreflight()
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	action := "pin"
	if request.Operation == core.PinsRemove {
		action = "unpin"
	}
	err := client.Chat(irc.ChatRequest{Action: action, ID: request.ParentID})
	stopped := stopClose()
	cancelWrite()
	if !stopped {
		<-closed
	}
	if err != nil || writeCtx.Err() == context.DeadlineExceeded {
		return failed(a, core.UpstreamUnavailable, true, true)
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWait()
	seen := false
	wireBytes := 0
	for {
		select {
		case <-waitCtx.Done():
			return failed(a, core.DeliveryUncertain, true, true)
		case event, ok := <-client.Events():
			if !ok {
				return failed(a, core.DeliveryUncertain, true, true)
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if seen || value.Action != action || value.Target != request.Target.Name || value.ID != request.ParentID || value.Message != nil || value.Profile != nil || value.Context != nil || value.From != "" || value.AccountID != "" || value.Text != "" || !value.ExpiresAt.IsZero() || value.SlowSeconds != 0 || value.HistoryLimit != 0 || len(value.Options) != 0 || len(value.Votes) != 0 || value.Closed {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > core.MaxPinAckBytes {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				wireBytes += wire
				seen = true
			case *irc.EndOfChatEvent:
				if !seen {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if wireBytes > core.MaxPinAckBytes-end {
					return failed(a, core.CapacityExceeded, true, true)
				}
				ack := core.PinAcknowledgement{Version: 1, Operation: request.Operation, Target: *request.Target, ParentID: request.ParentID}
				return accepted(waitCtx, a, core.Acceptance{Pin: &ack}, client.Nick())
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				if outcome, handled := numericFailure(a, value); handled {
					return outcome
				}
			default:
				continue
			}
		}
	}
}
