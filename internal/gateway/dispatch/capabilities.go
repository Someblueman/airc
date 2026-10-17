package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type Capabilities struct {
	Version int `json:"version"`
	store.Access
	MaxRequestFrameBytes  int           `json:"max_request_frame_bytes,omitempty"`
	MaxResponseFrameBytes int           `json:"max_response_frame_bytes,omitempty"`
	MaxRequestBytes       int           `json:"max_request_bytes"`
	UpstreamCompatible    *bool         `json:"upstream_compatible"`
	HistoryTargets        []core.Target `json:"history_targets,omitempty"`
	HistorySelectors      []string      `json:"history_selectors,omitempty"`
	MaxHistoryBytes       int           `json:"max_history_bytes,omitempty"`
	SearchTargets         []core.Target `json:"search_targets,omitempty"`
	SearchSelectors       []string      `json:"search_selectors,omitempty"`
	MaxSearchBytes        int           `json:"max_search_bytes,omitempty"`
	WatchTargets          []core.Target `json:"watch_targets,omitempty"`
	WatchSelectors        []string      `json:"watch_selectors,omitempty"`
	MaxWatchBytes         int           `json:"max_watch_bytes,omitempty"`
	WatchModes            []string      `json:"watch_modes,omitempty"`
	DirectoryModes        []string      `json:"directory_modes,omitempty"`
	DirectoryScope        string        `json:"directory_scope,omitempty"`
	MaxDirectoryBytes     int           `json:"max_directory_bytes,omitempty"`
	MaxDirectoryCards     int           `json:"max_directory_cards,omitempty"`
	ChannelsScope         string        `json:"channels_scope,omitempty"`
	MaxChannels           int           `json:"max_channels,omitempty"`
	MaxChannelsBytes      int           `json:"max_channels_bytes,omitempty"`
	AgentsScope           string        `json:"agents_scope,omitempty"`
	MaxAgents             int           `json:"max_agents,omitempty"`
	MaxAgentsBytes        int           `json:"max_agents_bytes,omitempty"`
	WatchMode             string        `json:"watch_mode,omitempty"`
	ContextTargets        []core.Target `json:"context_targets,omitempty"`
	MaxContextBytes       int           `json:"max_context_bytes,omitempty"`
	PinsTargets           []core.Target `json:"pins_targets,omitempty"`
	MaxPins               int           `json:"max_pins,omitempty"`
	MaxPinsBytes          int           `json:"max_pins_bytes,omitempty"`
	RoomTargets           []core.Target `json:"room_targets,omitempty"`
	MaxRoomBytes          int           `json:"max_room_bytes,omitempty"`
	WaitingTargets        []core.Target `json:"waiting_targets,omitempty"`
	MaxWaitingSignals     int           `json:"max_waiting_signals,omitempty"`
	MaxWaitingBytes       int           `json:"max_waiting_bytes,omitempty"`
	MaxCheckRooms         int           `json:"max_check_rooms,omitempty"`
	MaxCheckWaitSeconds   int           `json:"max_check_wait_seconds,omitempty"`
	MaxCheckBytes         int           `json:"max_check_bytes,omitempty"`
	// FollowSeconds and FollowKeepAliveSeconds describe a followed check
	// (check with stream: true on a connection carrier): its lease, and how
	// often a quiet follow sends an empty result.
	FollowSeconds           int           `json:"follow_seconds,omitempty"`
	FollowKeepAliveSeconds  int           `json:"follow_keepalive_seconds,omitempty"`
	FollowBatchMilliseconds int           `json:"follow_batch_ms,omitempty"`
	PollTargets             []core.Target `json:"poll_targets,omitempty"`
	MaxPollBytes            int           `json:"max_poll_bytes,omitempty"`
	TopicTargets            []core.Target `json:"topic_targets,omitempty"`
	MaxTopicBytes           int           `json:"max_topic_bytes,omitempty"`
	MembersTargets          []core.Target `json:"members_targets,omitempty"`
	MaxMembers              int           `json:"max_members,omitempty"`
	MaxMembersBytes         int           `json:"max_members_bytes,omitempty"`
	// Listener describes author access on this listener.
	Listener *ListenerCapabilities `json:"listener,omitempty"`
	// Carriers lists the ordered ways to reach this AIRC service, the same
	// entries aircd advertises in 005 CARRIERS (see docs/SERVICE.md).
	Carriers []string `json:"carriers,omitempty"`
}

// ListenerCapabilities is the author access policy a client may discover.
// AnyTarget means no target allowlist: the target lists above are then empty
// and AIRC alone decides what an author may do.
type ListenerCapabilities struct {
	Guests           bool `json:"guests"`
	PasswordRequired bool `json:"password_required"`
	AnyTarget        bool `json:"any_target"`
}

func (d *Dispatcher) capabilities(ctx context.Context, who actor) (Capabilities, error) {
	access, err := d.permissions(ctx, who)
	if err != nil {
		return Capabilities{}, err
	}
	enabled := d.operationsFor(who)
	access.Operations = core.EffectiveOperations(enabled, enabled, access.Operations, enabled)
	var roomTargets []core.Target
	if slices.Contains(access.Operations, core.HistoryRead) || slices.Contains(access.Operations, core.Search) || slices.Contains(access.Operations, core.Watch) || slices.Contains(access.Operations, core.EventsWatch) || slices.Contains(access.Operations, core.ContextRead) || slices.Contains(access.Operations, core.PinsRead) || slices.Contains(access.Operations, core.RoomRead) || slices.Contains(access.Operations, core.WaitingRead) || slices.Contains(access.Operations, core.PollResults) || slices.Contains(access.Operations, core.TopicRead) || slices.Contains(access.Operations, core.MembersRead) {
		for _, target := range access.Targets {
			if target.Kind == "room" {
				roomTargets = append(roomTargets, target)
			}
		}
		if len(roomTargets) == 0 && !access.anyTarget {
			access.Operations = slices.DeleteFunc(access.Operations, func(op core.Operation) bool {
				return op == core.HistoryRead || op == core.Search || op.IsWatch() || op == core.ContextRead || op == core.PinsRead || op == core.RoomRead || op == core.WaitingRead || op == core.PollResults || op == core.TopicRead || op == core.MembersRead
			})
		}
	}
	if access.Operations == nil {
		access.Operations = []core.Operation{}
	}
	result := Capabilities{Version: 1, Access: access.Access, MaxRequestBytes: core.MaxRequestBytes, Listener: who.listenerCapabilities(access)}
	if d.backend != nil {
		result.Carriers = d.backend.Carriers()
	}
	if slices.Contains(access.Operations, core.HistoryRead) {
		result.HistoryTargets, result.MaxHistoryBytes = roomTargets, backend.MaxHistoryBytes
		result.HistorySelectors = []string{"thread", "replies"}
	}
	if slices.Contains(access.Operations, core.Search) {
		result.SearchTargets, result.MaxSearchBytes = slices.Clone(roomTargets), backend.MaxHistoryBytes
		result.SearchSelectors = []string{"thread", "replies"}
	}
	if modes := watchCapabilityModes(access.Operations); len(modes) > 0 {
		result.WatchModes = modes
		result.WatchTargets, result.MaxWatchBytes, result.WatchMode = slices.Clone(roomTargets), backend.MaxHistoryBytes, "long_poll"
		result.WatchSelectors = []string{"thread", "replies"}
	}
	if slices.Contains(access.Operations, core.ContextRead) {
		result.ContextTargets = slices.Clone(roomTargets)
		result.MaxContextBytes = core.MaxContextBytes
	}
	if slices.Contains(access.Operations, core.PinsRead) {
		result.PinsTargets = slices.Clone(roomTargets)
		result.MaxPins = core.MaxPins
		result.MaxPinsBytes = core.MaxPinsBytes
	}
	if slices.Contains(access.Operations, core.RoomRead) {
		result.RoomTargets = slices.Clone(roomTargets)
		result.MaxRoomBytes = core.MaxRoomBytes
	}
	if slices.Contains(access.Operations, core.WaitingRead) {
		result.WaitingTargets = slices.Clone(roomTargets)
		result.MaxWaitingSignals = core.MaxWaitingSignals
		result.MaxWaitingBytes = core.MaxWaitingBytes
	}
	if slices.Contains(access.Operations, core.Check) {
		result.MaxCheckRooms = core.MaxCheckRooms
		result.MaxCheckWaitSeconds = core.MaxCheckWaitSeconds
		result.MaxCheckBytes = core.MaxCheckBytes
		if lease := d.backend.FollowLease(); lease > 0 {
			result.FollowSeconds = int(lease / time.Second)
			result.FollowKeepAliveSeconds = int(d.backend.FollowKeepAlive() / time.Second)
			result.FollowBatchMilliseconds = int(d.backend.FollowBatch() / time.Millisecond)
		}
	}
	if slices.Contains(access.Operations, core.PollResults) {
		result.PollTargets = slices.Clone(roomTargets)
		result.MaxPollBytes = core.MaxPollBytes
	}
	if slices.Contains(access.Operations, core.TopicRead) {
		result.TopicTargets = slices.Clone(roomTargets)
		result.MaxTopicBytes = core.MaxTopicBytes
	}
	if slices.Contains(access.Operations, core.MembersRead) {
		result.MembersTargets = slices.Clone(roomTargets)
		result.MaxMembers = core.MaxMembers
		result.MaxMembersBytes = core.MaxMembersBytes
	}
	if slices.Contains(access.Operations, core.Directory) {
		result.DirectoryScope = "public_global"
		result.DirectoryModes = []string{"snapshot", "nickname"}
		result.MaxDirectoryBytes = backend.MaxDirectoryBytes
		result.MaxDirectoryCards = 256
	}
	if slices.Contains(access.Operations, core.ChannelsRead) {
		result.ChannelsScope = "public_global"
		result.MaxChannels = core.MaxChannels
		result.MaxChannelsBytes = core.MaxChannelsBytes
	}
	if slices.Contains(access.Operations, core.AgentsRead) {
		result.AgentsScope = "public_global"
		result.MaxAgents = core.MaxAgents
		result.MaxAgentsBytes = core.MaxAgentsBytes
	}
	return result, nil
}

func watchCapabilityModes(operations []core.Operation) []string {
	if !slices.Contains(operations, core.Watch) && !slices.Contains(operations, core.EventsWatch) {
		return nil
	}
	return []string{"long_poll", "sse"}
}
