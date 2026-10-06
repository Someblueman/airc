package service

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
	"time"
)

// importAuthenticatedMessages admits verified intents and joins delivery before releasing the journal.
func importAuthenticatedMessages(ctx context.Context, c config.Config, messages []auth.AuthenticatedMessage) (report OfflineImportReport, failure error) {
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
