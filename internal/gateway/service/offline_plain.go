package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// importPlainRequests admits plain author requests from a local importer
// (offline bundles, QR sequences decoded to bundles, BPv7 files). The
// operator who runs the import is the access gate: each request names its
// author (a guest, or an account with its token), or runs as the bound
// author. Every request is checked before the journal opens; admission is
// durable per request, and a request ID the journal already holds for that
// author is a replay that admits nothing new, as on every carrier. Delivery
// workers join before the journal is released.
func importPlainRequests(ctx context.Context, c config.Config, listener config.Listener, raw []json.RawMessage, bound *config.BoundAuthor, check func(core.Submission) error) (report OfflineImportReport, failure error) {
	boundAuthor, err := loadBoundAuthor(bound)
	if err != nil {
		return report, err
	}
	subs := make([]core.Submission, len(raw))
	seen := make(map[[2]string]bool, len(raw))
	for i, body := range raw {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		sub, err := core.DecodeSubmission(body)
		if err != nil {
			return report, errors.New("offline import entry is not a valid contract request")
		}
		if sub.Request.Operation != core.Send {
			return report, errors.New("offline import only admits send requests")
		}
		author := boundAuthor.Author()
		if sub.Author != nil {
			author = *sub.Author
		}
		if !author.Valid() {
			return report, errors.New("offline import entry names no author and no bound author is configured")
		}
		if check != nil {
			if err := check(sub); err != nil {
				return report, err
			}
		}
		key := [2]string{author.Scope(), sub.Request.RequestID}
		if seen[key] {
			return report, store.ErrConflict
		}
		seen[key], subs[i] = true, sub
	}
	s, b, d, err := openListenerDispatch(ctx, c, listener)
	if err != nil {
		return report, err
	}
	defer b.Close()
	defer func() { failure = errors.Join(failure, s.Close()) }()
	report = OfflineImportReport{Version: 1, Entries: make([]OfflineImportEntry, 0, len(subs))}
	jobs := make([]dispatch.ImportedJob, 0, len(subs))
	replayed := false
	for _, sub := range subs {
		job, err := d.ImportDecoded(ctx, boundAuthor.Ingress(""), sub, time.Now())
		if err != nil {
			failure = err
			break
		}
		replayed = replayed || !job.Created
		jobs = append(jobs, job)
		report.Entries = append(report.Entries, OfflineImportEntry{Author: job.Author.Nick, Imported: job.Created, ReplayRejected: !job.Created, Result: job.Result})
	}
	failure = errors.Join(failure, runOfflineDelivery(ctx, c, s, b))
	for i := range jobs {
		if err := d.Refresh(ctx, &jobs[i]); err != nil {
			failure = errors.Join(failure, err)
			continue
		}
		report.Entries[i].Result = jobs[i].Result
	}
	if replayed {
		failure = errors.Join(failure, ErrBundleReplay)
	}
	return report, failure
}
