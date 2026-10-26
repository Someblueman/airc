package backend

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Members returns one complete native NAMES snapshot. The native server may
// legitimately return an empty list for an unknown or empty room.
func (b *Backend) Members(ctx context.Context, grant *store.ReadGrant) (core.MembersResult, error) {
	return readSnapshot(ctx, b, grant, roomRead(core.MembersRead), core.MaxMembersBytes, func(ctx context.Context, client *irc.Client, r core.Request) (core.MembersResult, error) {
		return boundedMembersRequest(ctx, client, r.Target.Name, core.MaxMembersBytes)
	})
}

func boundedMembersRequest(ctx context.Context, client *irc.Client, target string, maxBytes int) (core.MembersResult, error) {
	if err := client.Names(target); err != nil {
		return core.MembersResult{}, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	result := core.MembersResult{Target: core.Target{Kind: "room", Name: target}, Members: []core.Member{}}
	wireBytes := 0
	seen := map[string]bool{}
	finish := func() (core.MembersResult, error) {
		if ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
			return core.MembersResult{}, ErrReadUnavailable
		}
		sort.Slice(result.Members, func(i, j int) bool {
			li, lj := strings.ToLower(result.Members[i].Nick), strings.ToLower(result.Members[j].Nick)
			if li == lj {
				return result.Members[i].Nick < result.Members[j].Nick
			}
			return li < lj
		})
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) > maxBytes {
			return core.MembersResult{}, store.ErrCapacity
		}
		return result, nil
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return core.MembersResult{}, ErrReadUnavailable
			}
			raw, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			switch raw.Command {
			case "353":
				if len(raw.Params) != 3 || raw.Params[0] != client.Nick() || raw.Params[1] != "=" || raw.Params[2] != target || raw.Trailing == "" {
					return core.MembersResult{}, ErrReadUnavailable
				}
				names := strings.Fields(raw.Trailing)
				if len(names) == 0 || strings.Join(names, " ") != raw.Trailing {
					return core.MembersResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format("server", "353", raw.Params, raw.Trailing))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.MembersResult{}, store.ErrCapacity
				}
				for _, encoded := range names {
					operator := strings.HasPrefix(encoded, "@")
					nick := strings.TrimPrefix(encoded, "@")
					if nick == "" || strings.ContainsAny(nick, "@+ ") || !validNick(nick) {
						return core.MembersResult{}, ErrReadUnavailable
					}
					key := strings.ToLower(nick)
					if seen[key] {
						return core.MembersResult{}, ErrReadUnavailable
					}
					seen[key] = true
					if len(result.Members) >= core.MaxMembers {
						return core.MembersResult{}, store.ErrCapacity
					}
					result.Members = append(result.Members, core.Member{Nick: nick, Operator: operator})
				}
				wireBytes += wire
			case "366":
				if len(raw.Params) != 2 || raw.Params[0] != client.Nick() || raw.Params[1] != target || raw.Trailing != "End of NAMES list" {
					return core.MembersResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format("server", "366", raw.Params, raw.Trailing))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.MembersResult{}, store.ErrCapacity
				}
				wireBytes += wire
				return finish()
			default:
				// Registration numerics (001-005) can still be queued when the
				// one-shot command begins; they are unrelated to this snapshot.
				if raw.Command != "422" && len(raw.Command) == 3 && raw.Command[0] >= '4' && raw.Command[0] <= '5' {
					return core.MembersResult{}, ErrReadUnavailable
				}
				continue
			}
		case <-ctx.Done():
			return core.MembersResult{}, ErrReadUnavailable
		}
	}
}
