package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// ImportBPv7 binds the outer bundle metadata to the send it carries before
// opening the journal. Delayed import retains the bundle's original expiry.
func ImportBPv7(ctx context.Context, c config.Config, path string) (OfflineImportReport, error) {
	return ImportBPv7As(ctx, c, path, nil)
}

// ImportBPv7As imports one BPv7 file. The payload is a plain contract send
// request naming its author (or run as bound, else the listener's
// bound_author); the mapping fixes its target, and its request ID must be the
// bundle's identity.
func ImportBPv7As(ctx context.Context, c config.Config, path string, bound *config.BoundAuthor) (OfflineImportReport, error) {
	var empty OfflineImportReport
	if err := c.Validate(); err != nil {
		return empty, err
	}
	if err := c.CheckFiles(); err != nil {
		return empty, err
	}
	var mapping *config.BundleProtocol
	var listener config.Listener
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		if l.Protocol != "bpv7" || mapping != nil {
			return empty, errors.New("import-bpv7 requires exactly one enabled BPv7 mapping")
		}
		mapping, listener = l.BundleProtocol, l
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
	// Reject subsecond metadata rather than round validity into the
	// integer-second contract. Expired input never creates journal state.
	if bundle.Source != mapping.Source || bundle.Destination != mapping.Destination || bundle.CreationMilliseconds%1000 != 0 || bundle.LifetimeMilliseconds%1000 != 0 || bundle.IssuedAt() > now.Unix() || bundle.ExpiresAt() <= now.Unix() {
		return empty, errors.New("BPv7 mapping or original validity refused")
	}
	if bound == nil {
		bound = listener.BoundAuthor
	}
	return importPlainRequests(ctx, c, listener, []json.RawMessage{json.RawMessage(bundle.Payload)}, bound, func(sub core.Submission) error {
		r := sub.Request
		target := core.Target{}
		if r.Target != nil {
			target = *r.Target
		}
		if target.Normalize() != nil || target != mapping.Target || r.RequestID != bundle.RequestID() || r.IssuedAt != nil && *r.IssuedAt != bundle.IssuedAt() || r.ExpiresAt != nil && *r.ExpiresAt != bundle.ExpiresAt() {
			return errors.New("BPv7 metadata does not match the plain send")
		}
		return nil
	})
}
