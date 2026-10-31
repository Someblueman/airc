package backend

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/jsoninput"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func (b *Backend) Channels(ctx context.Context, grant *store.ReadGrant) (core.ChannelsResult, error) {
	valid := func(r core.Request) bool { return r.Operation == core.ChannelsRead }
	// The channel list's limit includes its line terminator.
	return readSnapshot(ctx, b, grant, valid, core.MaxChannelsBytes-1, func(ctx context.Context, client *irc.Client, _ core.Request) (core.ChannelsResult, error) {
		return boundedChannelsRequest(ctx, client, core.MaxChannelsBytes)
	})
}

func boundedChannelsRequest(ctx context.Context, client *irc.Client, maxBytes int) (core.ChannelsResult, error) {
	if err := client.ChannelsRaw(); err != nil {
		return core.ChannelsResult{}, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	result := core.ChannelsResult{Channels: []core.Channel{}}
	seen := map[string]bool{}
	wireBytes := 0
	finish := func() (core.ChannelsResult, error) {
		if ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
			return core.ChannelsResult{}, ErrReadUnavailable
		}
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded)+1 > maxBytes {
			return core.ChannelsResult{}, store.ErrCapacity
		}
		sort.Slice(result.Channels, func(i, j int) bool { return result.Channels[i].Name < result.Channels[j].Name })
		return result, nil
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return core.ChannelsResult{}, ErrReadUnavailable
			}
			raw, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			switch raw.Command {
			case "768":
				if len(result.Channels) >= core.MaxChannels {
					return core.ChannelsResult{}, store.ErrCapacity
				}
				if len(raw.Params) != 1 || raw.Params[0] != client.Nick() || raw.Trailing == "" {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				if len(raw.Tags) != 0 {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format(raw.Prefix, "768", raw.Params, raw.Trailing))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.ChannelsResult{}, store.ErrCapacity
				}
				channel, err := decodeChannel(raw.Trailing)
				if err != nil || !validChannelResult(channel) {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				if seen[channel.Name] {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				seen[channel.Name] = true
				wireBytes += wire
				result.Channels = append(result.Channels, channel)
			case "769":
				if len(raw.Params) != 1 || raw.Params[0] != client.Nick() || raw.Trailing != "End of channels list" {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				if len(raw.Tags) != 0 {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format(raw.Prefix, "769", raw.Params, raw.Trailing))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.ChannelsResult{}, store.ErrCapacity
				}
				return finish()
			default:
				if code, err := strconv.Atoi(raw.Command); err == nil && code >= 400 && code <= 599 && code != 422 {
					return core.ChannelsResult{}, ErrReadUnavailable
				}
				continue
			}
		case <-ctx.Done():
			return core.ChannelsResult{}, ErrReadUnavailable
		}
	}
}

func validChannelResult(channel core.Channel) bool {
	if !validRoom(channel.Name) || channel.Members < 0 || channel.Messages < 0 {
		return false
	}
	if !cardText(channel.Topic, 400) {
		return false
	}
	if channel.Messages > 0 && channel.LastActivity.IsZero() {
		return false
	}
	if channel.Messages == 0 && !channel.LastActivity.IsZero() {
		return false
	}
	return true
}

type channelWire struct {
	Name         string  `json:"name"`
	Members      *int    `json:"members"`
	Messages     *int    `json:"messages"`
	LastActivity *string `json:"last_activity"`
	Topic        string  `json:"topic"`
}

func decodeChannel(raw string) (core.Channel, error) {
	var wire channelWire
	if err := jsoninput.Decode([]byte(raw), &wire, core.MaxChannelsBytes); err != nil || wire.Members == nil || wire.Messages == nil {
		return core.Channel{}, ErrReadUnavailable
	}
	channel := core.Channel{Name: wire.Name, Members: *wire.Members, Messages: *wire.Messages, Topic: wire.Topic}
	if wire.LastActivity != nil {
		parsed, err := time.Parse(time.RFC3339Nano, *wire.LastActivity)
		if err != nil || parsed.IsZero() {
			return core.Channel{}, ErrReadUnavailable
		}
		channel.LastActivity = parsed.UTC()
	}
	return channel, nil
}
