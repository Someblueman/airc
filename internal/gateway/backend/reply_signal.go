package backend

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func replySignalExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if a.Kind != store.SendAttempt || request.Operation != core.ReplySignalSet && request.Operation != core.ReplySignalCancel || request.Target == nil || request.Target.Kind != "room" && request.Target.Kind != "direct" || !validHistoryID(request.ParentID) || request.Operation == core.ReplySignalSet && request.ReplySignal == nil || request.Operation == core.ReplySignalCancel && request.ReplySignal != nil {
		return failed(a, core.Forbidden, false, false)
	}
	normalized := request
	targetCopy := *request.Target
	normalized.Target = &targetCopy
	if request.ReplySignal != nil {
		inputCopy := *request.ReplySignal
		normalized.ReplySignal = &inputCopy
	}
	if err := normalized.Normalize(); err != nil || *normalized.Target != *request.Target || request.ReplySignal != nil && *normalized.ReplySignal != *request.ReplySignal {
		return failed(a, core.Forbidden, false, false)
	}
	request = normalized
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	proofCtx, cancelProof := context.WithTimeout(ctx, 5*time.Second)
	proofDeadline, _ := proofCtx.Deadline()
	closed := make(chan struct{})
	stopProof := context.AfterFunc(proofCtx, func() { _ = client.Close(); close(closed) })
	proof, err := boundedContextRequest(proofCtx, client, request.ParentID, 1, core.MaxContextBytes, request.Target.Kind)
	proofDone := stopProof()
	cancelProof()
	if !proofDone {
		<-closed
	}
	if !time.Now().Before(proofDeadline) {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			return failed(a, core.Forbidden, false, false)
		}
		if errors.Is(err, store.ErrCapacity) {
			return failed(a, core.CapacityExceeded, true, false)
		}
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	var actualTarget core.Target
	if request.Target.Kind == "room" {
		if _, err = validateContextProof(request, proof); err != nil {
			return failed(a, core.Forbidden, false, false)
		}
		actualTarget = *request.Target
	} else {
		actualTarget, err = validateReplySignalProof(request, proof, client.Nick())
		if err != nil {
			return failed(a, core.Forbidden, false, false)
		}
	}
	if ctx.Err() != nil || !time.Now().Before(proofDeadline) || !time.Now().Before(a.Deadline) {
		return failed(a, core.UpstreamUnavailable, true, false)
	}

	action := "prepare"
	if request.Operation == core.ReplySignalCancel {
		action = "cancel"
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closedWrite := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closedWrite) })
	chat := irc.ChatRequest{Action: action, ID: request.ParentID}
	if action == "prepare" {
		chat.Seconds, chat.Text = request.ReplySignal.TTLSeconds, request.ReplySignal.Note
	}
	writeErr := client.Chat(chat)
	stopped := stopClose()
	cancelWrite()
	if !stopped {
		<-closedWrite
	}
	if writeErr != nil || writeCtx.Err() == context.DeadlineExceeded {
		return failed(a, core.UpstreamUnavailable, true, true)
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWait()
	deadline, hasDeadline := waitCtx.Deadline()
	var signal *irc.ChatEntry
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
				if signal != nil || value.Action != action || value.Target != actualTarget.Name || value.ID != request.ParentID || value.From != client.Nick() || value.Message != nil || value.Profile != nil || value.Context != nil || value.SlowSeconds != 0 || value.HistoryLimit != 0 || len(value.Options) != 0 || len(value.Votes) != 0 || value.Closed || action == "cancel" && (value.AccountID != "" || value.Text != "" || value.ExpiresAt.IsZero()) || action == "prepare" && (value.AccountID == "" || !validHistoryID(value.AccountID) || !protocol.BriefText(value.Text, 240) || value.ExpiresAt.IsZero()) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > core.MaxReplySignalAckBytes {
					return failed(a, core.CapacityExceeded, true, true)
				}
				wireBytes += wire
				copy := value.ChatEntry
				signal = &copy
			case *irc.EndOfChatEvent:
				if signal == nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if wireBytes > core.MaxReplySignalAckBytes-end {
					return failed(a, core.CapacityExceeded, true, true)
				}
				ack := core.ReplySignalAcknowledgement{Version: 1, Operation: request.Operation, Target: *request.Target, ParentID: request.ParentID, NativeTarget: actualTarget.Name, Action: signal.Action, From: signal.From, AccountID: signal.AccountID, Note: signal.Text, ExpiresAt: signal.ExpiresAt}
				if _, err := core.ReplySignalAckBytes(&ack, request, client.Nick()); err != nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				observed := time.Now()
				if waitCtx.Err() != nil || hasDeadline && !observed.Before(deadline) || !observed.Before(a.Deadline) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				return Outcome{Attempt: a, ReplySignal: &ack, ObservedAt: observed}
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

func validateReplySignalProof(request core.Request, proof irc.ConversationContext, nick string) (core.Target, error) {
	if proof.TriggerID != request.ParentID || len(proof.Messages) != 1 || proof.Messages[0].ID != request.ParentID || !validHistoryID(proof.RootID) || containsString(proof.Missing, request.ParentID) || !validContextSummary(proof) {
		return core.Target{}, auth.ErrForbidden
	}
	message := proof.Messages[0]
	if !validReplySignalMessage(message) {
		return core.Target{}, auth.ErrForbidden
	}
	root := message.ThreadID
	if root == "" {
		root = message.ID
	}
	if root != proof.RootID {
		return core.Target{}, auth.ErrForbidden
	}
	requested := *request.Target
	if requested.Normalize() != nil {
		return core.Target{}, auth.ErrForbidden
	}
	actual := core.Target{Kind: requested.Kind, Name: message.Target}
	normalizedActual := actual
	if normalizedActual.Normalize() != nil {
		return core.Target{}, auth.ErrForbidden
	}
	author := core.Target{Kind: "direct", Name: nick}
	if author.Normalize() != nil {
		return core.Target{}, auth.ErrForbidden
	}
	if requested.Kind == "room" {
		if normalizedActual != requested {
			return core.Target{}, auth.ErrForbidden
		}
		return actual, nil
	}
	from := core.Target{Kind: "direct", Name: message.From}
	normalizedFrom := from
	if normalizedFrom.Normalize() != nil || (normalizedFrom != author && normalizedFrom != requested) || (normalizedActual != author && normalizedActual != requested) {
		return core.Target{}, auth.ErrForbidden
	}
	return actual, nil
}

func validReplySignalMessage(message irc.MessageMetadata) bool {
	if !validContextMessageMetadata(message) || !protocol.BriefText(message.Kind, 32) || !validNick(message.From) {
		return false
	}
	target := core.Target{Kind: "room", Name: message.Target}
	if target.Normalize() != nil {
		target = core.Target{Kind: "direct", Name: message.Target}
		if target.Normalize() != nil {
			return false
		}
	}
	return true
}
