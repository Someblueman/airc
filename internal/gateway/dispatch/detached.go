package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// DetachedResult has exactly one operation-specific result on success.
type DetachedResult struct {
	Version      int                        `json:"version"`
	Operation    core.Operation             `json:"operation"`
	Result       *core.Result               `json:"result,omitempty"`
	Page         *backend.HistoryPage       `json:"page,omitempty"`
	Directory    *backend.DirectorySnapshot `json:"directory,omitempty"`
	Channels     *core.ChannelsResult       `json:"channels,omitempty"`
	Agents       *core.AgentsResult         `json:"agents,omitempty"`
	Context      *core.ContextResult        `json:"context,omitempty"`
	Pins         *core.PinsResult           `json:"pins,omitempty"`
	Room         *core.RoomResult           `json:"room,omitempty"`
	Waiting      *core.WaitingResult        `json:"waiting,omitempty"`
	Poll         *core.PollResult           `json:"poll,omitempty"`
	Topic        *core.TopicResult          `json:"topic,omitempty"`
	Members      *core.MembersResult        `json:"members,omitempty"`
	Capabilities *Capabilities              `json:"capabilities,omitempty"`
}

func (d *Dispatcher) Detached(ctx context.Context, token string, encrypted bool, now time.Time) (DetachedResult, error) {
	verified, err := d.AuthenticateDetached(token, encrypted, now)
	if err != nil {
		return DetachedResult{}, err
	}
	return d.DetachedVerified(ctx, verified, now)
}

// DetachedVerified executes intent authenticated by this dispatcher, allowing a
// packet adapter to resolve its owner-controlled response route first.
func (d *Dispatcher) DetachedVerified(ctx context.Context, verified VerifiedMessage, now time.Time) (DetachedResult, error) {
	message, err := verified.forDispatcher(d, now)
	if err != nil {
		return DetachedResult{}, err
	}
	result := DetachedResult{Version: 1, Operation: message.Request.Operation}
	if message.Request.Operation == core.CapabilitiesGet {
		readCtx, cancel := context.WithDeadline(ctx, time.Unix(message.ReplayUntil, 0))
		defer cancel()
		capabilities, err := d.capabilities(readCtx, message.Identity)
		if err != nil {
			return DetachedResult{}, err
		}
		result.Capabilities = &capabilities
		return result, nil
	}
	switch message.Request.Operation {
	case core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.EventsWatch, core.Directory, core.ChannelsRead, core.AgentsRead, core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.PollResults, core.TopicRead, core.MembersRead:
		// Reads cannot extend the signed validity-plus-skew boundary. A detached
		// read remains a one-shot lease; renewal must authenticate a new envelope.
		readCtx, cancel := context.WithDeadline(ctx, time.Unix(message.ReplayUntil, 0))
		defer cancel()
		if message.Request.Operation == core.ReceiptGet {
			delivery, e := d.execute(readCtx, message.Identity, message.Request, message.ReplayUntil, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Result = &delivery
		} else if message.Request.Operation == core.Directory {
			snapshot, e := d.directory(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Directory = &snapshot
		} else if message.Request.Operation == core.ChannelsRead {
			channels, e := d.readChannels(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Channels = &channels
		} else if message.Request.Operation == core.AgentsRead {
			agents, e := d.readAgents(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Agents = &agents
		} else if message.Request.Operation == core.ContextRead {
			contextResult, e := d.readContext(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Context = &contextResult
		} else if message.Request.Operation == core.PinsRead {
			pins, e := d.readPins(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Pins = &pins
		} else if message.Request.Operation == core.RoomRead {
			room, e := d.readRoom(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Room = &room
		} else if message.Request.Operation == core.WaitingRead {
			waiting, e := d.readWaiting(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Waiting = &waiting
		} else if message.Request.Operation == core.PollResults {
			poll, e := d.readPoll(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Poll = &poll
		} else if message.Request.Operation == core.TopicRead {
			topic, e := d.readTopic(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Topic = &topic
		} else if message.Request.Operation == core.MembersRead {
			members, e := d.readMembers(readCtx, message.Identity, message.Request, now)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Members = &members
		} else {
			page, e := d.readPage(readCtx, message.Identity, message.Request, "", now, nil)
			if e != nil {
				return DetachedResult{}, e
			}
			result.Page = &page
		}
	default:
		delivery, e := d.execute(ctx, message.Identity, message.Request, message.ReplayUntil, now)
		if e != nil {
			return DetachedResult{}, e
		}
		result.Result = &delivery
	}
	return result, nil
}
