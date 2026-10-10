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

// Room reads the current native room settings without mutating them.
func (b *Backend) Room(ctx context.Context, grant *store.ReadGrant) (core.RoomResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.RoomResult{}, err
	}
	if a.Request.Operation != core.RoomRead || a.Request.Target == nil || a.Request.Target.Kind != "room" {
		return core.RoomResult{}, store.ErrInvalid
	}
	var result core.RoomResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		if client.Features()["CHAT"] != "1" {
			return ErrReadUnsupported
		}
		settings, err := boundedRoomRequest(overall, client, a.Request.Target.Name, core.MaxRoomBytes)
		if err != nil {
			return err
		}
		candidate := core.RoomResult{Target: *a.Request.Target, SlowSeconds: settings.SlowSeconds, HistoryLimit: settings.HistoryLimit}
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > core.MaxRoomBytes {
			return store.ErrCapacity
		}
		deadline, _ := overall.Deadline()
		if overall.Err() != nil || !time.Now().Before(deadline) {
			return ErrReadUnavailable
		}
		result = candidate
		return nil
	})
	if err != nil {
		return core.RoomResult{}, err
	}
	return result, nil
}

func boundedRoomRequest(ctx context.Context, client *irc.Client, target string, maxBytes int) (core.RoomResult, error) {
	if err := client.Chat(irc.ChatRequest{Action: "room", Target: target, Seconds: -1, Limit: -1}); err != nil {
		return core.RoomResult{}, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	var result core.RoomResult
	seen := false
	wireBytes := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return core.RoomResult{}, ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if seen || value.Action != "room" || value.Target != target || value.Message != nil || value.Profile != nil || value.Context != nil || value.ID != "" || value.From != "" || value.AccountID != "" || value.Text != "" || !value.ExpiresAt.IsZero() || value.SlowSeconds < 0 || value.SlowSeconds > 3600 || value.HistoryLimit < 0 || len(value.Options) != 0 || len(value.Votes) != 0 || value.Closed {
					return core.RoomResult{}, ErrReadUnavailable
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return core.RoomResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.RoomResult{}, store.ErrCapacity
				}
				wireBytes += wire
				result = core.RoomResult{Target: core.Target{Kind: "room", Name: target}, SlowSeconds: value.SlowSeconds, HistoryLimit: value.HistoryLimit}
				seen = true
			case *irc.EndOfChatEvent:
				if !seen || ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
					return core.RoomResult{}, ErrReadUnavailable
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if end > maxBytes || wireBytes > maxBytes-end {
					return core.RoomResult{}, store.ErrCapacity
				}
				return result, nil
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" || value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return core.RoomResult{}, ErrReadUnavailable
				}
			default:
				continue
			}
		case <-ctx.Done():
			return core.RoomResult{}, ErrReadUnavailable
		}
	}
}
