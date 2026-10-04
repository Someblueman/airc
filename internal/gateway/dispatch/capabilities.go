package dispatch

import (
	"context"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type Capabilities struct {
	Version int `json:"version"`
	store.Access
	MaxRequestFrameBytes  int           `json:"max_request_frame_bytes,omitempty"`
	MaxResponseFrameBytes int           `json:"max_response_frame_bytes,omitempty"`
	MaxEnvelopeBytes      int           `json:"max_envelope_bytes"`
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
	PollTargets           []core.Target `json:"poll_targets,omitempty"`
	MaxPollBytes          int           `json:"max_poll_bytes,omitempty"`
	TopicTargets          []core.Target `json:"topic_targets,omitempty"`
	MaxTopicBytes         int           `json:"max_topic_bytes,omitempty"`
	MembersTargets        []core.Target `json:"members_targets,omitempty"`
	MaxMembers            int           `json:"max_members,omitempty"`
	MaxMembersBytes       int           `json:"max_members_bytes,omitempty"`
}

// Capabilities describes available gateway intake, not a promise of upstream
// acceptance. Compatibility is unknown until runtime negotiation is integrated.
func (d *Dispatcher) Capabilities(ctx context.Context, token string) (Capabilities, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return Capabilities{}, err
	}
	return d.capabilities(ctx, identity)
}

func (d *Dispatcher) capabilities(ctx context.Context, identity auth.Identity) (Capabilities, error) {
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return Capabilities{}, err
	}
	access.Operations = core.EffectiveOperations(d.operations, d.operations, access.Operations, d.operations)
	var roomTargets []core.Target
	if slices.Contains(access.Operations, core.HistoryRead) || slices.Contains(access.Operations, core.Search) || slices.Contains(access.Operations, core.Watch) || slices.Contains(access.Operations, core.ContextRead) || slices.Contains(access.Operations, core.PinsRead) || slices.Contains(access.Operations, core.RoomRead) || slices.Contains(access.Operations, core.WaitingRead) || slices.Contains(access.Operations, core.PollResults) || slices.Contains(access.Operations, core.TopicRead) || slices.Contains(access.Operations, core.MembersRead) {
		for _, target := range access.Targets {
			if target.Kind == "room" {
				roomTargets = append(roomTargets, target)
			}
		}
		if len(roomTargets) == 0 {
			access.Operations = slices.DeleteFunc(access.Operations, func(op core.Operation) bool {
				return op == core.HistoryRead || op == core.Search || op == core.Watch || op == core.ContextRead || op == core.PinsRead || op == core.RoomRead || op == core.WaitingRead || op == core.PollResults || op == core.TopicRead || op == core.MembersRead
			})
		}
	}
	if access.Operations == nil {
		access.Operations = []core.Operation{}
	}
	result := Capabilities{Version: 1, Access: access, MaxEnvelopeBytes: core.MaxEnvelopeBytes}
	if slices.Contains(access.Operations, core.HistoryRead) {
		result.HistoryTargets, result.MaxHistoryBytes = roomTargets, backend.MaxHistoryBytes
		result.HistorySelectors = []string{"thread", "replies"}
	}
	if slices.Contains(access.Operations, core.Search) {
		result.SearchTargets, result.MaxSearchBytes = slices.Clone(roomTargets), backend.MaxHistoryBytes
		result.SearchSelectors = []string{"thread", "replies"}
	}
	if slices.Contains(access.Operations, core.Watch) {
		result.WatchModes = []string{"long_poll", "sse"}
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
	return result, nil
}
