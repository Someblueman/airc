package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/pgnotifywire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const maxPostgresNotifyBytes = 7999

// ServePostgresNotify owns one serial mTLS PostgreSQL LISTEN session. Each
// NOTIFY payload is a plain contract request posted as the author it names,
// else the route's bound author: the database role allowed to NOTIFY on the
// channel is the access gate. Payloads are ephemeral; refused or failed
// messages are discarded after one bounded admission attempt and are never
// acknowledged or published here.
func ServePostgresNotify(ctx context.Context, b config.PostgresNotify, security *tls.Config, author dispatch.RouteAuthor, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if ctx == nil || security == nil || d == nil || cap(slots) < 1 || cap(requests) < 1 {
		return store.ErrInvalid
	}
	backoff := time.Second
	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}

		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		session, err := pgnotifywire.Connect(connectCtx, b.Address, security.Clone(), b.Database, b.Username, b.Channel)
		cancel()
		if err == nil && session == nil {
			err = store.ErrInvalid
		}
		if session != nil {
			if err == nil {
				err = postgresNotifySession(ctx, session, b, author, d, requests, &backoff)
			}
			closeErr := session.Close()
			if err == nil && ctx.Err() == nil {
				err = closeErr
			}
		}
		<-slots
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, store.ErrClosed) {
			return err
		}
		if !postgresNotifyWait(ctx, backoff) {
			return nil
		}
		backoff = min(backoff*2, 15*time.Second)
	}
	return nil
}

func postgresNotifySession(ctx context.Context, session *pgnotifywire.Session, b config.PostgresNotify, author dispatch.RouteAuthor, d *dispatch.Dispatcher, requests chan struct{}, backoff *time.Duration) error {
	for ctx.Err() == nil {
		payload, err := session.Next(ctx)
		if err != nil {
			clear(payload)
			return err
		}
		*backoff = time.Second
		if len(payload) == 0 || len(payload) > maxPostgresNotifyBytes {
			clear(payload)
			continue
		}
		if err := admitPostgresNotify(ctx, d, author, dispatch.RouteSource(b.ID), b.Route, payload, requests); errors.Is(err, store.ErrClosed) {
			clear(payload)
			return err
		}
		clear(payload)
	}
	return ctx.Err()
}

func admitPostgresNotify(parent context.Context, d *dispatch.Dispatcher, author dispatch.RouteAuthor, source string, route config.PostgresNotifyRoute, payload []byte, requests chan struct{}) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return nil
	}
	sub, err := core.DecodeSubmission(payload)
	if err != nil || sub.Request.Operation != core.Send || !routeTargetMatches(sub.Request, route.Target) {
		return nil
	}
	// Only a closed journal ends the owner; any other outcome is discarded.
	if _, err := d.SubmitRouteDecoded(ctx, author, source, sub, time.Now()); errors.Is(err, store.ErrClosed) {
		return err
	}
	return nil
}

func postgresNotifyWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
