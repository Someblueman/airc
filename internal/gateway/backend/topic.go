package backend

import (
	"context"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Topic preserves native topic metadata independently of retained chat history.
func (b *Backend) Topic(ctx context.Context, grant *store.ReadGrant) (core.TopicResult, error) {
	return readSnapshot(ctx, b, grant, roomRead(core.TopicRead), core.MaxTopicBytes, func(ctx context.Context, client *irc.Client, r core.Request) (core.TopicResult, error) {
		return boundedTopicRequest(ctx, client, r.Target.Name, core.MaxTopicBytes)
	})
}

func boundedTopicRequest(ctx context.Context, client *irc.Client, target string, maxBytes int) (core.TopicResult, error) {
	if err := client.Topic(target); err != nil {
		return core.TopicResult{}, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	result := core.TopicResult{Target: core.Target{Kind: "room", Name: target}}
	seen := false
	wireBytes := 0
	finish := func() (core.TopicResult, error) {
		if ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
			return core.TopicResult{}, ErrReadUnavailable
		}
		return result, nil
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return core.TopicResult{}, ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.TopicEvent:
				if seen || value.Channel != target || value.SetBy != "" || !protocol.BriefText(value.Topic, 400) {
					return core.TopicResult{}, ErrReadUnavailable
				}
				command, text := "332", value.Topic
				if text == "" {
					command, text = "331", "No topic is set"
				}
				wire := len(protocol.Format("server", command, []string{client.Nick(), target}, text))
				if wire > maxBytes {
					return core.TopicResult{}, store.ErrCapacity
				}
				wireBytes = wire
				result.Topic = value.Topic
				seen = true
				if value.Topic == "" {
					return finish()
				}
			case *irc.RawEvent:
				if value.Command == "333" {
					if !seen || len(value.Params) != 4 || value.Params[0] != client.Nick() || value.Params[1] != target || !validNick(value.Params[2]) || value.Trailing != "" {
						return core.TopicResult{}, ErrReadUnavailable
					}
					seconds, err := strconv.ParseInt(value.Params[3], 10, 64)
					if err != nil || strconv.FormatInt(seconds, 10) != value.Params[3] {
						return core.TopicResult{}, ErrReadUnavailable
					}
					setAt := time.Unix(seconds, 0).UTC()
					if _, err := setAt.MarshalJSON(); err != nil || setAt.IsZero() {
						return core.TopicResult{}, ErrReadUnavailable
					}
					wire := len(protocol.Format("server", "333", value.Params, ""))
					if wire > maxBytes || wireBytes > maxBytes-wire {
						return core.TopicResult{}, store.ErrCapacity
					}
					result.SetBy, result.SetAt = value.Params[2], setAt
					return finish()
				}
				if value.Command == "331" || value.Command == "332" || value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return core.TopicResult{}, ErrReadUnavailable
				}
			}
		case <-ctx.Done():
			return core.TopicResult{}, ErrReadUnavailable
		}
	}
}
