package adapter

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func bearerCall(ctx context.Context, d *dispatch.Dispatcher, token string, operation core.Operation, body []byte) (any, error) {
	now := time.Now()
	switch operation {
	case core.CapabilitiesGet:
		return d.Capabilities(ctx, token)
	case core.ContextRead:
		return d.ReadContext(ctx, token, body, now)
	case core.PinsRead:
		return d.ReadPins(ctx, token, body, now)
	case core.RoomRead:
		return d.ReadRoom(ctx, token, body, now)
	case core.WaitingRead:
		return d.ReadWaiting(ctx, token, body, now)
	case core.PollResults:
		return d.ReadPoll(ctx, token, body, now)
	case core.TopicRead:
		return d.ReadTopic(ctx, token, body, now)
	case core.MembersRead:
		return d.ReadMembers(ctx, token, body, now)
	case core.Directory:
		return d.Directory(ctx, token, body, now)
	case core.ChannelsRead:
		return d.ReadChannels(ctx, token, body, now)
	case core.AgentsRead:
		return d.ReadAgents(ctx, token, body, now)
	case core.HistoryRead, core.Search:
		return d.ReadPage(ctx, token, body, "", now, nil)
	case core.Watch, core.EventsWatch:
		return d.ReadPage(ctx, token, body, "", now, nil)
	default:
		if bearerMutation(operation) {
			return d.Mutation(ctx, token, body, now)
		}
		return d.Session(ctx, token, body, now)
	}
}

func bearerMutation(operation core.Operation) bool {
	switch operation {
	case core.Send, core.Reply, core.ReactionAdd, core.ProfileUpdate, core.PresenceSet, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.ActivitySet, core.ReplySignalSet, core.ReplySignalCancel, core.PollCreate, core.PollVote, core.PollClose, core.MessageAction, core.MessageCorrect, core.MessageRetract:
		return true
	default:
		return false
	}
}
