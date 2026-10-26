package backend

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/jsoninput"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Agents returns one complete native AGENTS snapshot. The native response is
// accumulated privately and is published only after its 764 terminator.
func (b *Backend) Agents(ctx context.Context, grant *store.ReadGrant) (core.AgentsResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.AgentsResult{}, err
	}
	if a.Request.Operation != core.AgentsRead {
		return core.AgentsResult{}, store.ErrInvalid
	}
	var result core.AgentsResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		candidate, err := boundedAgentsRequest(overall, client, core.MaxAgentsBytes, a.Request.Limit)
		if err != nil {
			return err
		}
		result = candidate
		return nil
	})
	if err != nil {
		return core.AgentsResult{}, err
	}
	return result, nil
}

type agentWire struct {
	Nick        *string   `json:"nick"`
	Channels    *[]string `json:"channels"`
	ConnectedAt *string   `json:"connected_at"`
}

func boundedAgentsRequest(ctx context.Context, client *irc.Client, maxBytes, limit int) (core.AgentsResult, error) {
	if limit < 1 || limit > core.MaxAgents {
		return core.AgentsResult{}, store.ErrInvalid
	}
	if err := client.AgentsRaw(); err != nil {
		return core.AgentsResult{}, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	result := core.AgentsResult{Version: 1, Agents: []irc.AgentInfo{}}
	seen := map[string]bool{}
	wireBytes := 0
	finish := func() (core.AgentsResult, error) {
		if ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
			return core.AgentsResult{}, ErrReadUnavailable
		}
		sort.Slice(result.Agents, func(i, j int) bool {
			li, lj := strings.ToLower(result.Agents[i].Nick), strings.ToLower(result.Agents[j].Nick)
			if li == lj {
				return result.Agents[i].Nick < result.Agents[j].Nick
			}
			return li < lj
		})
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded)+1 > maxBytes {
			return core.AgentsResult{}, store.ErrCapacity
		}
		if ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
			return core.AgentsResult{}, ErrReadUnavailable
		}
		return result, nil
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return core.AgentsResult{}, ErrReadUnavailable
			}
			raw, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			switch raw.Command {
			case "763":
				if len(raw.Params) != 1 || raw.Params[0] != client.Nick() || raw.Trailing == "" || len(raw.Tags) != 0 {
					return core.AgentsResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format(raw.Prefix, raw.Command, raw.Params, raw.Trailing))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.AgentsResult{}, store.ErrCapacity
				}
				var value agentWire
				if err := jsoninput.Decode([]byte(raw.Trailing), &value, maxBytes); err != nil || value.Nick == nil || value.Channels == nil || value.ConnectedAt == nil {
					return core.AgentsResult{}, ErrReadUnavailable
				}
				connectedAt, err := time.Parse(time.RFC3339Nano, *value.ConnectedAt)
				if err != nil || connectedAt.IsZero() || !validNick(*value.Nick) || seen[strings.ToLower(*value.Nick)] {
					return core.AgentsResult{}, ErrReadUnavailable
				}
				channels := append([]string{}, (*value.Channels)...)
				sort.Strings(channels)
				for i, channel := range channels {
					if !validRoom(channel) {
						return core.AgentsResult{}, ErrReadUnavailable
					}
					if i > 0 && channels[i-1] == channel {
						return core.AgentsResult{}, ErrReadUnavailable
					}
				}
				seen[strings.ToLower(*value.Nick)] = true
				result.Agents = append(result.Agents, irc.AgentInfo{Nick: *value.Nick, Channels: channels, ConnectedAt: connectedAt.UTC()})
				if len(result.Agents) > limit || len(result.Agents) > core.MaxAgents {
					return core.AgentsResult{}, store.ErrCapacity
				}
				wireBytes += wire
			case "764":
				if len(raw.Params) != 1 || raw.Params[0] != client.Nick() || raw.Trailing != "End of agents list" || len(raw.Tags) != 0 {
					return core.AgentsResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format(raw.Prefix, raw.Command, raw.Params, raw.Trailing))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.AgentsResult{}, store.ErrCapacity
				}
				return finish()
			default:
				if rejectedRead(raw) {
					return core.AgentsResult{}, ErrReadUnavailable
				}
			}
		case <-ctx.Done():
			return core.AgentsResult{}, ErrReadUnavailable
		}
	}
}
