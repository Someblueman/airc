package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var ErrBundleReplay = errors.New("offline bundle contains previously imported requests")

type OfflineImportEntry struct {
	Principal      string      `json:"principal"`
	Binding        string      `json:"binding"`
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
	if err := c.Validate(); err != nil {
		return report, err
	}
	if err := c.CheckFiles(); err != nil {
		return report, err
	}
	enabled := 0
	for _, l := range c.Listeners {
		if l.Enabled {
			enabled++
			if l.Protocol != "offline_bundle" {
				return report, errors.New("import-bundle requires an offline bundle configuration")
			}
		}
	}
	if enabled != 1 {
		return report, errors.New("import-bundle requires exactly one enabled offline bundle")
	}
	bundle, err := adapter.ReadOfflineBundle(path)
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
	b, err := backend.New(c)
	if err != nil {
		return report, err
	}
	s, err := store.Open(ctx, c)
	if err != nil {
		return report, err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	report = OfflineImportReport{Version: 1, Entries: make([]OfflineImportEntry, 0, len(messages))}
	replayed := false
	for _, m := range messages {
		job, created, err := s.Admit(ctx, m.Identity, m.Request, m.ReplayUntil, time.Now())
		if err != nil {
			failure = err
			break
		}
		replayed = replayed || !created
		report.Entries = append(report.Entries, OfflineImportEntry{Principal: job.Principal, Binding: job.Binding, Imported: created, ReplayRejected: !created, Result: job.Result})
	}
	failure = errors.Join(failure, runOfflineDelivery(ctx, c, s, b))
	for i := range report.Entries {
		job, err := s.Lookup(ctx, messages[i].Identity, messages[i].Request.RequestID)
		if err == nil {
			report.Entries[i].Result = job.Result
		} else {
			failure = errors.Join(failure, err)
		}
	}
	if replayed {
		failure = errors.Join(failure, ErrBundleReplay)
	}
	return report, failure
}
