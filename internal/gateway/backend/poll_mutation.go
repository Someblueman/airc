package backend

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func pollMutationExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if request.Target == nil || request.Target.Kind != "room" || request.Operation != core.PollCreate && request.Operation != core.PollVote && request.Operation != core.PollClose {
		return failed(a, core.Forbidden, false, false)
	}
	if request.Operation == core.PollCreate && request.Poll == nil || request.Operation != core.PollCreate && request.Poll != nil {
		return failed(a, core.Forbidden, false, false)
	}
	if ctx.Err() != nil || !time.Now().Before(a.Deadline) {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	if request.Operation != core.PollCreate {
		proofCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		proofDeadline, _ := proofCtx.Deadline()
		closedProof := make(chan struct{})
		stopProof := context.AfterFunc(proofCtx, func() { _ = client.Close(); close(closedProof) })
		proof, err := boundedPollRequest(proofCtx, client, request.Target.Name, request.PollID, core.MaxPollBytes)
		proofTimedOut := proofCtx.Err() != nil
		stoppedProof := stopProof()
		cancel()
		if !stoppedProof {
			<-closedProof
		}
		if errors.Is(err, errPollScope) {
			return failed(a, core.Forbidden, false, false)
		}
		var nativeErr *pollNativeError
		if errors.As(err, &nativeErr) {
			if outcome, handled := numericFailure(a, nativeErr.event); handled {
				outcome.Completion.MayHavePosted = false
				return outcome
			}
		}
		if err != nil {
			return failed(a, core.UpstreamUnavailable, true, false)
		}
		if proof.ID != request.PollID || proof.Target.Name != request.Target.Name {
			return failed(a, core.Forbidden, false, false)
		}
		if proofTimedOut || !time.Now().Before(proofDeadline) || !time.Now().Before(a.Deadline) {
			return failed(a, core.UpstreamUnavailable, true, false)
		}
	}

	chat := irc.ChatRequest{Target: request.Target.Name}
	switch request.Operation {
	case core.PollCreate:
		chat.Action = "poll"
		chat.Text = request.Poll.Question
		chat.Options = append([]string(nil), request.Poll.Options...)
		chat.Seconds = request.Poll.TTLSeconds
	case core.PollVote:
		chat.Action, chat.ID, chat.Choice = "vote", request.PollID, request.Choice
	case core.PollClose:
		chat.Action, chat.ID = "close-poll", request.PollID
	}
	if ctx.Err() != nil || !time.Now().Before(a.Deadline) {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	err := client.Chat(chat)
	writeTimedOut := writeCtx.Err() != nil
	stopped := stopClose()
	cancelWrite()
	if !stopped {
		<-closed
	}
	if err != nil || writeTimedOut {
		return failed(a, core.UpstreamUnavailable, true, true)
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWait()
	var result *core.PollResult
	var receipt *irc.SendReceiptEvent
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
			case *irc.SendReceiptEvent:
				if request.Operation != core.PollCreate || receipt != nil || value.Receipt == nil || value.Type != "send_receipt" {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				copy := *value
				receipt = &copy
				if encoded, marshalErr := json.Marshal(value); marshalErr != nil {
					return failed(a, core.CapacityExceeded, true, true)
				} else {
					line := len(protocol.Format("server", "762", []string{client.Nick(), request.Target.Name}, string(encoded)))
					if line > core.MaxPollAckBytes || wireBytes > core.MaxPollAckBytes-line {
						return failed(a, core.CapacityExceeded, true, true)
					}
					wireBytes += line
				}
			case *irc.ChatEvent:
				if result != nil || request.Operation == core.PollCreate && receipt == nil || !validPollEntry(value.ChatEntry, request.Target.Name, value.ID) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				candidate := core.PollResult{Target: core.Target{Kind: "room", Name: value.Target}, ID: value.ID, From: value.From, AccountID: value.AccountID, Question: value.Text, Options: append([]string(nil), value.Options...), Votes: append([]int(nil), value.Votes...), ExpiresAt: value.ExpiresAt, Closed: value.Closed}
				result = &candidate
				encoded, marshalErr := json.Marshal(value.ChatEntry)
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(encoded)))
				if marshalErr != nil || wire > core.MaxPollAckBytes || wireBytes > core.MaxPollAckBytes-wire {
					return failed(a, core.CapacityExceeded, true, true)
				}
				wireBytes += wire
			case *irc.EndOfChatEvent:
				if result == nil || request.Operation == core.PollCreate && receipt == nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if wireBytes > core.MaxPollAckBytes-end {
					return failed(a, core.CapacityExceeded, true, true)
				}
				ack := core.PollAcknowledgement{Version: 1, Operation: request.Operation, Target: *request.Target, Action: "results", Poll: *result, Receipt: receipt}
				return accepted(waitCtx, a, core.Acceptance{Poll: &ack}, client.Nick())
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" || value.Command == "762" {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				if outcome, handled := numericFailure(a, value); handled {
					if receipt != nil || result != nil {
						uncertain := failed(a, core.DeliveryUncertain, true, true)
						uncertain.Completion.Failure.UpstreamCode = outcome.Completion.Failure.UpstreamCode
						return uncertain
					}
					return outcome
				}
			}
		}
	}
}
