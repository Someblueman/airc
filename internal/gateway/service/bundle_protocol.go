package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// ImportBPv7 binds the outer bundle metadata to a verified signed send before
// opening the journal. Delayed import retains the original signed expiry.
func ImportBPv7(ctx context.Context, c config.Config, path string) (OfflineImportReport, error) {
	var empty OfflineImportReport
	if err := c.Validate(); err != nil {
		return empty, err
	}
	if err := c.CheckFiles(); err != nil {
		return empty, err
	}
	var mapping *config.BundleProtocol
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		if l.Protocol != "bpv7" || mapping != nil {
			return empty, errors.New("import-bpv7 requires exactly one enabled BPv7 mapping")
		}
		mapping = l.BundleProtocol
	}
	if mapping == nil {
		return empty, errors.New("import-bpv7 requires exactly one enabled BPv7 mapping")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	bundle, err := adapter.ReadBPv7(path)
	if err != nil {
		return empty, err
	}
	now := time.Now()
	// Reject subsecond metadata rather than round validity into the signed
	// integer-second contract. Expired input never creates journal state.
	if bundle.Source != mapping.Source || bundle.Destination != mapping.Destination || bundle.CreationMilliseconds%1000 != 0 || bundle.LifetimeMilliseconds%1000 != 0 || bundle.IssuedAt() > now.Unix() || bundle.ExpiresAt() <= now.Unix() {
		return empty, errors.New("BPv7 mapping or original validity refused")
	}
	v, err := auth.New(c)
	if err != nil {
		return empty, err
	}
	m, err := v.AuthenticateSigned(bundle.Payload, now)
	if err != nil {
		return empty, err
	}
	r := m.Request
	if m.Identity.ID() != mapping.Principal || r.Operation != core.Send || r.Target == nil || *r.Target != mapping.Target || r.RequestID != bundle.RequestID() || r.IssuedAt == nil || *r.IssuedAt != bundle.IssuedAt() || r.ExpiresAt == nil || *r.ExpiresAt != bundle.ExpiresAt() {
		return empty, errors.New("BPv7 metadata does not match the authenticated send")
	}
	return importAuthenticatedMessages(ctx, c, []auth.AuthenticatedMessage{m})
}
