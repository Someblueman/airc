package dispatch

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type Capabilities struct {
	Version int `json:"version"`
	store.Access
	MaxEnvelopeBytes   int   `json:"max_envelope_bytes"`
	UpstreamCompatible *bool `json:"upstream_compatible"`
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
	if access.Operations == nil {
		access.Operations = []core.Operation{}
	}
	return Capabilities{Version: 1, Access: access, MaxEnvelopeBytes: core.MaxEnvelopeBytes}, nil
}
