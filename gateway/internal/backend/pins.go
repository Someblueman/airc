package backend

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Pins reads the durable pin snapshot for one authorized room. The native
// CHAT stream is owned exclusively by withRead and is accepted only after its
// terminator has been received.
func (b *Backend) Pins(ctx context.Context, grant *store.ReadGrant) (core.PinsResult, error) {
	return readSnapshot(ctx, b, grant, roomRead(core.PinsRead), core.MaxPinsBytes, func(ctx context.Context, client *irc.Client, r core.Request) (core.PinsResult, error) {
		pins, err := boundedPinsRequest(ctx, client, r.Target.Name)
		return core.PinsResult{Target: *r.Target, Pins: pins}, err
	})
}

func boundedPinsRequest(ctx context.Context, client *irc.Client, target string) ([]irc.MessageMetadata, error) {
	return boundedPinsRequestBudget(ctx, client, target, core.MaxPinsBytes)
}

func boundedPinsRequestBudget(ctx context.Context, client *irc.Client, target string, maxBytes int) ([]irc.MessageMetadata, error) {
	if err := client.Chat(irc.ChatRequest{Action: "pins", Target: target}); err != nil {
		return nil, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	pins := make([]irc.MessageMetadata, 0, core.MaxPins)
	seen := make(map[string]bool)
	wireBytes := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if value.Action != "pin" || !pinEntryShape(value.ChatEntry) || value.Target != target || value.Message == nil || value.Message.Target != target {
					return nil, ErrReadUnavailable
				}
				if value.ID != value.Message.ID || value.From != value.Message.From || !validContextMessage(*value.Message, target) {
					return nil, ErrReadUnavailable
				}
				if seen[value.Message.ID] {
					return nil, ErrReadUnavailable
				}
				if len(pins) >= core.MaxPins {
					return nil, store.ErrCapacity
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return nil, ErrReadUnavailable
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return nil, store.ErrCapacity
				}
				wireBytes += wire
				seen[value.Message.ID] = true
				pins = append(pins, *value.Message)
			case *irc.EndOfChatEvent:
				if (hasDeadline && !time.Now().Before(deadline)) || ctx.Err() != nil {
					return nil, ErrReadUnavailable
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if end > maxBytes || wireBytes > maxBytes-end {
					return nil, store.ErrCapacity
				}
				return pins, nil
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" {
					return nil, ErrReadUnavailable
				}
				if value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return nil, ErrReadUnavailable
				}
			default:
				// Unrelated notices and live events may share the connection while
				// the bounded CHAT response is outstanding.
				continue
			}
		case <-ctx.Done():
			return nil, ErrReadUnavailable
		}
	}
}

func pinEntryShape(entry irc.ChatEntry) bool {
	return entry.Profile == nil && entry.Context == nil && entry.Text == "" && entry.AccountID == "" && entry.ExpiresAt.IsZero() && entry.SlowSeconds == 0 && entry.HistoryLimit == 0 && len(entry.Options) == 0 && len(entry.Votes) == 0 && !entry.Closed
}
