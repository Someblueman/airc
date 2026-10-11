package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

var ErrBundleReplay = errors.New("offline bundle contains previously imported requests")

type OfflineImportEntry struct {
	// Author is the nickname the request posted as.
	Author         string      `json:"author,omitempty"`
	Imported       bool        `json:"imported"`
	ReplayRejected bool        `json:"replay_rejected"`
	Result         core.Result `json:"result"`
}

type OfflineImportReport struct {
	Version int                  `json:"version"`
	Entries []OfflineImportEntry `json:"entries"`
}

// ImportOfflineBundle checks every entry before opening the journal.
// Admission is durable per entry, not atomic for the batch; partial reports
// survive later errors. Replays do not admit new work. Delivery workers join
// before journal release, including when the report contains rejected replays.
func ImportOfflineBundle(ctx context.Context, c config.Config, path string) (report OfflineImportReport, failure error) {
	return ImportOfflineBundleAs(ctx, c, path, nil)
}

// ImportOfflineBundleAs imports a bundle of plain author requests. bound,
// when set, overrides the listener's bound_author as the author of requests
// that name none.
func ImportOfflineBundleAs(ctx context.Context, c config.Config, path string, bound *config.BoundAuthor) (report OfflineImportReport, failure error) {
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
				return report, errors.New("bundle import requires an offline bundle configuration")
			}
		}
	}
	if enabled != 1 {
		return report, errors.New("bundle import requires exactly one enabled offline bundle")
	}
	bundle, err := adapter.ReadOfflineBundle(path)
	if err != nil {
		return report, err
	}
	if bound == nil {
		bound = listener.BoundAuthor
	}
	return importPlainRequests(ctx, c, listener, bundle.Requests, bound, nil)
}
