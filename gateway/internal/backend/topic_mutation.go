package backend

import (
	"context"
	"strconv"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func topicExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if request.Target == nil || request.Target.Kind != "room" || request.Topic == nil {
		return failed(a, core.Forbidden, false, false)
	}
	if ctx.Err() != nil {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	err := client.SetTopic(request.Target.Name, *request.Topic)
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
	finish := func(result core.TopicResult) Outcome {
		return accepted(waitCtx, a, core.Acceptance{Topic: &core.TopicAcknowledgement{Version: 1, Operation: request.Operation, Topic: result}}, client.Nick())
	}
	var result core.TopicResult
	seenTopic := false
	for {
		select {
		case <-waitCtx.Done():
			return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
		case event, ok := <-client.Events():
			if !ok {
				return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
			}
			switch value := event.(type) {
			case *irc.TopicEvent:
				// The server broadcasts TOPIC before its private reply. The SDK
				// represents private 331/332 replies as TopicEvent with no SetBy;
				// broadcasts have a nonempty setter and are only noise here.
				if value.SetBy != "" {
					continue
				}
				if seenTopic || value.Channel != request.Target.Name || !protocol.BriefText(value.Topic, 400) || value.Topic != *request.Topic {
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				}
				if value.Topic == "" {
					return finish(core.TopicResult{Target: *request.Target})
				}
				result = core.TopicResult{Target: *request.Target, Topic: value.Topic}
				seenTopic = true
			case *irc.RawEvent:
				switch value.Command {
				case "331", "332":
					return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
				case "333":
					if !seenTopic || len(value.Params) != 4 || value.Params[0] != client.Nick() || value.Params[1] != request.Target.Name || value.Params[2] != client.Nick() || value.Trailing != "" {
						return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
					}
					seconds, err := strconv.ParseInt(value.Params[3], 10, 64)
					if err != nil || strconv.FormatInt(seconds, 10) != value.Params[3] {
						return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
					}
					setAt := time.Unix(seconds, 0).UTC()
					if setAt.IsZero() {
						return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
					}
					result.SetBy, result.SetAt = value.Params[2], setAt
					return finish(result)
				default:
					if outcome, handled := numericFailure(a, value); handled {
						return outcome
					}
				}
			}
		}
	}
}
