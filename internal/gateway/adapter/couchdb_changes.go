package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/couchdbwire"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeCouchDBChanges admits only signed Send intents from the configured
// namespace. Feed progress moves after every non-control change is durable.
func ServeCouchDBChanges(ctx context.Context, b config.CouchDBChanges, client *couchdbwire.Client, realm, binding string, d *dispatch.Dispatcher, journal *store.Store, requests chan struct{}) error {
	if ctx == nil || client == nil || d == nil || journal == nil || realm == "" || binding == "" || cap(requests) < 1 {
		return store.ErrInvalid
	}
	scope := config.CouchDBChangesScope(realm, binding, b)
	progress, err := journal.ProvisionCouchDBProgress(ctx, b.Namespace, scope, b.NormalizedInitialSequence())
	if err != nil {
		return err
	}
	backoff := time.Second
	for ctx.Err() == nil {
		pass, cancel := context.WithTimeout(ctx, 60*time.Second)
		progress, err = couchDBChangesPass(pass, b, client, scope, binding, d, journal, requests, progress)
		cancel()
		if errors.Is(err, store.ErrClosed) || errors.Is(err, store.ErrConflict) {
			return err
		}
		delay := time.Second
		if err == nil {
			backoff = time.Second
		} else {
			delay = backoff
			backoff = min(backoff*2, 15*time.Second)
		}
		if !couchDBChangesWait(ctx, delay) {
			return nil
		}
	}
	return nil
}

func couchDBChangesPass(ctx context.Context, b config.CouchDBChanges, client *couchdbwire.Client, scope, binding string, d *dispatch.Dispatcher, journal *store.Store, requests chan struct{}, progress store.CouchDBProgress) (store.CouchDBProgress, error) {
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return progress, ctx.Err()
	}
	page, err := client.Changes(ctx, progress.Sequence)
	if err != nil {
		return progress, err
	}
	if page.LastSequence == "" || len(page.Changes) > 16 || len(page.Changes) != 0 && page.LastSequence == progress.Sequence {
		return progress, couchdbwire.ErrRequest
	}
	for _, change := range page.Changes {
		if change.Control {
			continue
		}
		if change.Namespace != b.Namespace || len(change.Token) == 0 || len(change.Token) > MaxRequestFrameBytes {
			return progress, couchdbwire.ErrRequest
		}
		if err := admitCouchDBChange(ctx, d, b, binding, change.Token); err != nil {
			return progress, err
		}
	}
	if page.LastSequence == progress.Sequence {
		return progress, nil
	}
	next, err := journal.AdvanceCouchDBProgress(ctx, b.Namespace, scope, progress, page.LastSequence)
	if err != nil {
		return progress, err
	}
	return next, nil
}

func admitCouchDBChange(parent context.Context, d *dispatch.Dispatcher, b config.CouchDBChanges, binding, token string) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	verified, err := d.AuthenticateDetached(token, true, time.Now())
	if err != nil {
		result, retainedBinding, retainedErr := d.RetainedEncryptedSend(ctx, token, b.Route.Principal, b.Route.Target, time.Now())
		if retainedErr == nil && retainedBinding == binding && result.Result != nil && result.Result.Durable {
			return nil
		}
		return auth.ErrAuthentication
	}
	target, ok := verified.Target()
	if !ok || target != b.Route.Target || verified.PrincipalID() != b.Route.Principal || verified.Binding() != binding || verified.Operation() != core.Send {
		return auth.ErrForbidden
	}
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err != nil {
		return err
	}
	if result.Result == nil || !result.Result.Durable || result.Result.RequestID != verified.RequestID() {
		return store.ErrInvalid
	}
	return nil
}

func couchDBChangesWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
