package backend

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func activityExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if a.Kind != store.SendAttempt || request.Operation != core.ActivitySet || request.Target == nil || request.Activity == nil || (request.Target.Kind != "room" && request.Target.Kind != "direct") || (request.Activity.Kind != "typing" && request.Activity.Kind != "thinking") || request.Activity.TTLSeconds < 1 || request.Activity.TTLSeconds > 15 || !protocol.BriefText(request.Activity.Note, 240) {
		return failed(a, core.Forbidden, false, false)
	}
	normalized := request
	targetCopy := *request.Target
	activityCopy := *request.Activity
	normalized.Target = &targetCopy
	normalized.Activity = &activityCopy
	if err := normalized.Normalize(); err != nil || *normalized.Target != *request.Target || *normalized.Activity != *request.Activity {
		return failed(a, core.Forbidden, false, false)
	}
	request = normalized
	target := *request.Target
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	err := client.Chat(irc.ChatRequest{Action: request.Activity.Kind, Target: target.Name, Seconds: request.Activity.TTLSeconds, Text: request.Activity.Note})
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
	deadline, hasDeadline := waitCtx.Deadline()
	var entry *irc.ChatEntry
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
				if entry != nil || value.Target != target.Name || value.From != client.Nick() || value.ID != "" || value.Message != nil || value.Profile != nil || value.Context != nil || value.AccountID == "" || !protocol.BriefText(value.Text, 240) || value.ExpiresAt.IsZero() || value.SlowSeconds != 0 || value.HistoryLimit != 0 || len(value.Options) != 0 || len(value.Votes) != 0 || value.Closed || (value.Action != "typing" && value.Action != "thinking") {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > core.MaxActivityAckBytes {
					return failed(a, core.CapacityExceeded, true, true)
				}
				wireBytes += wire
				copy := value.ChatEntry
				entry = &copy
			case *irc.EndOfChatEvent:
				if entry == nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if wireBytes > core.MaxActivityAckBytes-end {
					return failed(a, core.CapacityExceeded, true, true)
				}
				ack := core.ActivityAcknowledgement{Version: 1, Operation: request.Operation, Target: target, Kind: entry.Action, From: entry.From, AccountID: entry.AccountID, Note: entry.Text, ExpiresAt: entry.ExpiresAt}
				if _, err := core.ActivityAckBytes(&ack, request, client.Nick()); err != nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				observed := time.Now()
				if waitCtx.Err() != nil || hasDeadline && !observed.Before(deadline) || !observed.Before(a.Deadline) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				return Outcome{Attempt: a, Activity: &ack, ObservedAt: observed}
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
