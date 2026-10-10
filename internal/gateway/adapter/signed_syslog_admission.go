package adapter

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func admitSignedSyslog(ctx context.Context, d *dispatch.Dispatcher, batch *auth.VerifiedSyslogBatch, requests chan struct{}) error {
	if cap(requests) < 1 || ctx.Err() != nil {
		return store.ErrCapacity
	}
	select {
	case requests <- struct{}{}:
	default:
		return store.ErrCapacity
	}
	defer func() { <-requests }()
	work, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	results, err := d.SignedSyslog(work, batch, time.Now())
	if err != nil {
		return err
	}
	if len(results) != len(batch.Records) {
		return store.ErrInvalid
	}
	for _, result := range results {
		if !result.Durable {
			return store.ErrInvalid
		}
	}
	return nil
}
