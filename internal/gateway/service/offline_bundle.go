package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var ErrBundleReplay = errors.New("offline bundle contains previously imported requests")

type OfflineImportEntry struct {
	// Author is the nickname a plain request posted as; Principal and
	// Binding name a signed envelope's identity.
	Author         string      `json:"author,omitempty"`
	Principal      string      `json:"principal,omitempty"`
	Binding        string      `json:"binding,omitempty"`
	Imported       bool        `json:"imported"`
	ReplayRejected bool        `json:"replay_rejected"`
	Result         core.Result `json:"result"`
}

type OfflineImportReport struct {
	Version int                  `json:"version"`
	Entries []OfflineImportEntry `json:"entries"`
}

// ImportOfflineBundle authenticates every entry before opening the journal.
// Admission is durable per entry, not atomic for the batch; partial reports
// survive later errors. Replays do not admit new work. Delivery workers join
// before journal release, including when the report contains rejected replays.
func ImportOfflineBundle(ctx context.Context, c config.Config, path string) (report OfflineImportReport, failure error) {
	return ImportOfflineBundleAs(ctx, c, path, nil)
}

// ImportOfflineBundleAs imports a bundle of plain author requests (or signed
// envelopes). bound, when set, overrides the listener's bound_author as the
// author of plain requests that name none.
func ImportOfflineBundleAs(ctx context.Context, c config.Config, path string, bound *config.BoundAuthor) (report OfflineImportReport, failure error) {
	return importSignedBundle(ctx, c, path, func(path string) (adapter.OfflineBundle, error) {
		bundle, err := adapter.ReadOfflineBundle(path)
		if err == nil && len(bundle.Requests) > 0 {
			return bundle, errPlainBundle
		}
		return bundle, err
	}, bound)
}

// errPlainBundle routes a bundle of plain requests to importPlainRequests.
var errPlainBundle = errors.New("plain offline bundle")

func importSignedBundle(ctx context.Context, c config.Config, path string, read func(string) (adapter.OfflineBundle, error), bound *config.BoundAuthor) (report OfflineImportReport, failure error) {
	if err := c.Validate(); err != nil {
		return report, err
	}
	if err := c.CheckFiles(); err != nil {
		return report, err
	}
	enabled := 0
	var listener config.Listener
	for _, l := range c.Listeners {
		if l.Enabled {
			enabled++
			listener = l
			if l.Protocol != "offline_bundle" {
				return report, errors.New("signed bundle import requires an offline bundle configuration")
			}
		}
	}
	if enabled != 1 {
		return report, errors.New("signed bundle import requires exactly one enabled offline bundle")
	}
	bundle, err := read(path)
	if errors.Is(err, errPlainBundle) {
		override := listener.BoundAuthor
		if bound != nil {
			override = bound
		}
		return importPlainRequests(ctx, c, listener, bundle.Requests, override, nil)
	}
	if err != nil {
		return report, err
	}
	v, err := auth.New(c)
	if err != nil {
		return report, err
	}
	messages := make([]auth.AuthenticatedMessage, len(bundle.Envelopes))
	seen := make(map[[3]string]bool)
	for i, token := range bundle.Envelopes {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		m, err := v.AuthenticateSigned(token, time.Now())
		if err != nil {
			return report, err
		}
		if m.Request.Operation != core.Send {
			return report, errors.New("offline bundle only imports signed send requests")
		}
		key := [3]string{m.Identity.ID(), m.Identity.Binding(), m.Request.RequestID}
		if seen[key] {
			return report, store.ErrConflict
		}
		seen[key], messages[i] = true, m
	}
	return importAuthenticatedMessages(ctx, c, messages)
}
