package adapter

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeMatrix serially owns one recovery identity and its bounded HTTP work.
// Cursor failures retry from durable state; no error authorizes a new baseline.
func ServeMatrix(ctx context.Context, server *auth.MatrixServer, d *dispatch.Dispatcher, journal *store.Store, slots, requests chan struct{}) error {
	if ctx == nil || server == nil || d == nil || journal == nil || cap(slots) < 1 || cap(requests) < 1 {
		return store.ErrInvalid
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return nil
	}
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		progress, err := AdvanceMatrix(ctx, server, d, journal, requests)
		delay := time.Second
		if err != nil {
			delay = backoff
			backoff = min(backoff*2, 10*time.Second)
		} else {
			backoff = 250 * time.Millisecond
			if progress {
				delay = 100 * time.Millisecond
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
