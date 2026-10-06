package adapter

import (
	"context"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

type mcpDiscovery struct {
	Version          int                      `json:"version"`
	Tools            []string                 `json:"tools"`
	Targets          map[string][]core.Target `json:"targets"`
	ReceiptLookup    bool                     `json:"receipt_lookup"`
	Limits           config.Limits            `json:"limits"`
	Retry            config.Retry             `json:"retry_defaults"`
	MaxEnvelopeBytes int                      `json:"max_envelope_bytes"`
	MaxHistoryBytes  int                      `json:"max_history_bytes,omitempty"`
	MaxWatchBytes    int                      `json:"max_watch_bytes,omitempty"`
}

func mcpDiscoveryFor(ctx context.Context, d *dispatch.Dispatcher, token string) (mcpDiscovery, error) {
	caps, err := d.Capabilities(ctx, token)
	if err != nil {
		return mcpDiscovery{}, err
	}
	result := mcpDiscovery{Version: 1, Tools: []string{}, Targets: map[string][]core.Target{}, ReceiptLookup: slices.Contains(caps.Operations, core.ReceiptGet), Limits: caps.Limits, Retry: caps.Retry, MaxEnvelopeBytes: caps.MaxEnvelopeBytes}
	if slices.Contains(caps.Operations, core.Send) && len(caps.Targets) > 0 {
		result.Tools = append(result.Tools, "send")
		result.Targets["send"] = caps.Targets
	}
	if slices.Contains(caps.Operations, core.HistoryRead) && len(caps.HistoryTargets) > 0 {
		result.Tools = append(result.Tools, "read")
		result.Targets["read"], result.MaxHistoryBytes = caps.HistoryTargets, caps.MaxHistoryBytes
	}
	if slices.Contains(caps.Operations, core.Watch) && len(caps.WatchTargets) > 0 {
		result.Tools = append(result.Tools, "wait")
		result.Targets["wait"], result.MaxWatchBytes = caps.WatchTargets, caps.MaxWatchBytes
	}
	result.Tools = append(result.Tools, "status")
	return result, nil
}
