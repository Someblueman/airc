package dispatch

import (
	"context"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type Capabilities struct {
	Version int `json:"version"`
	store.Access
	MaxEnvelopeBytes   int           `json:"max_envelope_bytes"`
	UpstreamCompatible *bool         `json:"upstream_compatible"`
	HistoryTargets     []core.Target `json:"history_targets,omitempty"`
	MaxHistoryBytes    int           `json:"max_history_bytes,omitempty"`
}

// Capabilities describes available gateway intake, not a promise of upstream
// acceptance. Compatibility is unknown until runtime negotiation is integrated.
func (d *Dispatcher) Capabilities(ctx context.Context, token string) (Capabilities, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return Capabilities{}, err
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return Capabilities{}, err
	}
	access.Operations = core.EffectiveOperations(d.operations, d.operations, access.Operations, d.operations)
	var historyTargets []core.Target
	if slices.Contains(access.Operations, core.HistoryRead) {
		for _, target := range access.Targets {
			if target.Kind == "room" {
				historyTargets = append(historyTargets, target)
			}
		}
		if len(historyTargets) == 0 {
			access.Operations = slices.DeleteFunc(access.Operations, func(op core.Operation) bool { return op == core.HistoryRead })
		}
	}
	if access.Operations == nil {
		access.Operations = []core.Operation{}
	}
	result := Capabilities{Version: 1, Access: access, MaxEnvelopeBytes: core.MaxEnvelopeBytes, HistoryTargets: historyTargets}
	if len(historyTargets) > 0 {
		result.MaxHistoryBytes = backend.MaxHistoryBytes
	}
	return result, nil
}
