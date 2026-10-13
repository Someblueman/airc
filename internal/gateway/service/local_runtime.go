package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// soleListener returns c's only enabled listener, and whether there is
// exactly one and it serves protocol.
func soleListener(c config.Config, protocol string) (config.Listener, bool) {
	var found config.Listener
	enabled := 0
	for _, l := range c.Listeners {
		if l.Enabled {
			enabled++
			found = l
		}
	}
	return found, enabled == 1 && found.Protocol == protocol
}

// openListenerDispatch opens the backend, cursor codec and journal for one
// listener's dispatcher. The caller closes the store.
func openListenerDispatch(ctx context.Context, c config.Config, l config.Listener) (*store.Store, *backend.Backend, *dispatch.Dispatcher, error) {
	b, err := backend.New(c)
	if err != nil {
		return nil, nil, nil, err
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		return nil, nil, nil, err
	}
	s, err := store.Open(ctx, c)
	if err != nil {
		return nil, nil, nil, err
	}
	d, err := dispatch.ForListener(s, b, cursors, l)
	if err != nil {
		return nil, nil, nil, errors.Join(err, s.Close())
	}
	return s, b, d, nil
}

// waitDelivered polls until the journal has no pending delivery or grace
// ends, returning a failed pending check made before grace ended.
func waitDelivered(grace context.Context, s *store.Store) error {
	for grace.Err() == nil {
		pending, err := s.HasPending(grace)
		if err != nil {
			if grace.Err() == nil {
				return err
			}
			return nil
		}
		if !pending {
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-grace.Done():
			timer.Stop()
		}
	}
	return nil
}
