package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func runOfflineDelivery(ctx context.Context, c config.Config, s *store.Store, b *backend.Backend) error {
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { err := runner.Run(workCtx); cancel(); done <- err }()
	grace, stop := context.WithTimeout(workCtx, 5*time.Second)
	defer stop()
	var failure error
	for grace.Err() == nil {
		pending, err := s.HasPending(grace)
		if err != nil {
			if grace.Err() == nil {
				failure = err
			}
			break
		}
		if !pending {
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-grace.Done():
			timer.Stop()
		}
	}
	cancel()
	return errors.Join(failure, ctx.Err(), <-done)
}
