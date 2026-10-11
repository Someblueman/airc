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
	// Account is AIRC's answer to account.register and account.identify.
	Account *backend.Account `json:"account,omitempty"`
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
	// Reads cannot extend the signed validity-plus-skew boundary. Durable
	// mutations keep the caller's context and their original journal lifetime.
	// Route requests have no signed boundary; the caller's context bounds them.
	readCtx := ctx
	if !message.Request.Operation.IsMutation() && message.ReplayUntil != 0 {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithDeadline(ctx, time.Unix(message.ReplayUntil, 0))
		defer cancel()
	}
	switch message.Request.Operation {
	case core.CapabilitiesGet:
		result.Capabilities = new(Capabilities)
		*result.Capabilities, err = d.capabilities(readCtx, principal(message.Identity))
	case core.ReceiptGet:
		result.Result = new(core.Result)
		*result.Result, err = d.execute(readCtx, message.Identity, message.Request, message.ReplayUntil, now)
	case core.Directory:
		result.Directory = new(backend.DirectorySnapshot)
		*result.Directory, err = d.directory(readCtx, principal(message.Identity), message.Request, now)
	case core.ChannelsRead:
		result.Channels = new(core.ChannelsResult)
		*result.Channels, err = d.readChannels(readCtx, principal(message.Identity), message.Request, now)
	case core.AgentsRead:
		result.Agents = new(core.AgentsResult)
		*result.Agents, err = d.readAgents(readCtx, principal(message.Identity), message.Request, now)
	case core.ContextRead:
		result.Context = new(core.ContextResult)
		*result.Context, err = d.readContext(readCtx, principal(message.Identity), message.Request, now)
	case core.PinsRead:
		result.Pins = new(core.PinsResult)
		*result.Pins, err = d.readPins(readCtx, principal(message.Identity), message.Request, now)
	case core.RoomRead:
		result.Room = new(core.RoomResult)
		*result.Room, err = d.readRoom(readCtx, principal(message.Identity), message.Request, now)
	case core.WaitingRead:
		result.Waiting = new(core.WaitingResult)
		*result.Waiting, err = d.readWaiting(readCtx, principal(message.Identity), message.Request, now)
	case core.PollResults:
		result.Poll = new(core.PollResult)
		*result.Poll, err = d.readPoll(readCtx, principal(message.Identity), message.Request, now)
	case core.TopicRead:
		result.Topic = new(core.TopicResult)
		*result.Topic, err = d.readTopic(readCtx, principal(message.Identity), message.Request, now)
	case core.MembersRead:
		result.Members = new(core.MembersResult)
		*result.Members, err = d.readMembers(readCtx, principal(message.Identity), message.Request, now)
	case core.HistoryRead, core.Search, core.Watch, core.EventsWatch:
		result.Page = new(backend.HistoryPage)
		*result.Page, err = d.readPage(readCtx, principal(message.Identity), message.Request, "", now, nil)
	default:
		result.Result = new(core.Result)
		*result.Result, err = d.execute(ctx, message.Identity, message.Request, message.ReplayUntil, now)
	}
	if err != nil {
		return DetachedResult{}, err
	}
	return result, nil
}
