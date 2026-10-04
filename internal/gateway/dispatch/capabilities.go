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
	WatchMode             string        `json:"watch_mode,omitempty"`
	ContextTargets        []core.Target `json:"context_targets,omitempty"`
	MaxContextBytes       int           `json:"max_context_bytes,omitempty"`
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
	if slices.Contains(access.Operations, core.HistoryRead) || slices.Contains(access.Operations, core.Search) || slices.Contains(access.Operations, core.Watch) || slices.Contains(access.Operations, core.ContextRead) {
		for _, target := range access.Targets {
			if target.Kind == "room" {
				roomTargets = append(roomTargets, target)
			}
		}
		if len(roomTargets) == 0 {
			access.Operations = slices.DeleteFunc(access.Operations, func(op core.Operation) bool {
				return op == core.HistoryRead || op == core.Search || op == core.Watch || op == core.ContextRead
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
	if slices.Contains(access.Operations, core.Directory) {
		result.DirectoryScope = "public_global"
		result.DirectoryModes = []string{"snapshot", "nickname"}
		result.MaxDirectoryBytes = backend.MaxDirectoryBytes
		result.MaxDirectoryCards = 256
	}
	return result, nil
}
