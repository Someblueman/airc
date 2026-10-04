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

func roomExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if request.Target == nil || request.Target.Kind != "room" || request.Room == nil {
		return failed(a, core.Forbidden, false, false)
	}
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	seconds, limit := int64(-1), -1
	if request.Room.SlowSeconds != nil {
		seconds = *request.Room.SlowSeconds
	}
	if request.Room.HistoryLimit != nil {
		limit = *request.Room.HistoryLimit
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	err := client.Chat(irc.ChatRequest{Action: "room", Target: request.Target.Name, Seconds: seconds, Limit: limit})
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
	deadline, hasDeadline := waitCtx.Deadline()
	var result core.RoomResult
	seen := false
	wireBytes := 0
	finish := func() Outcome {
		ack := core.RoomAcknowledgement{Version: 1, Operation: request.Operation, Room: result}
		if _, err := core.RoomAckBytes(&ack, request); err != nil {
			return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
		}
		observed := time.Now()
		if waitCtx.Err() != nil || hasDeadline && !observed.Before(deadline) || !observed.Before(a.Deadline) {
			return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
		}
		return Outcome{Attempt: a, Room: &ack, ObservedAt: observed}
	}
	for {
		select {
		case <-waitCtx.Done():
			return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
		case event, ok := <-client.Events():
			if !ok {
				return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if seen || value.Action != "room" || value.Target != request.Target.Name || value.Message != nil || value.Profile != nil || value.Context != nil || value.ID != "" || value.From != "" || value.AccountID != "" || value.Text != "" || !value.ExpiresAt.IsZero() || value.SlowSeconds < 0 || value.SlowSeconds > 3600 || value.HistoryLimit < 0 || len(value.Options) != 0 || len(value.Votes) != 0 || value.Closed {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > core.MaxRoomBytes {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				wireBytes += wire
				result = core.RoomResult{Target: *request.Target, SlowSeconds: value.SlowSeconds, HistoryLimit: value.HistoryLimit}
				seen = true
			case *irc.EndOfChatEvent:
				if !seen {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if wireBytes > core.MaxRoomBytes-end {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				return finish()
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				if outcome, handled := numericFailure(a, value); handled {
					return outcome
				}
			}
		}
	}
}
