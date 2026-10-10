//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// RunFileMailbox processes one existing input with the ordinary journal and
// joined delivery workers. Result publication reports intake, not final delivery.
func RunFileMailbox(ctx context.Context, c config.Config) (failure error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	var listener config.Listener
	count := 0
	for _, l := range c.Listeners {
		if l.Enabled {
			count++
			listener = l
		}
	}
	if count != 1 || listener.Protocol != "file_mailbox" {
		return errors.New("mailbox-once requires exactly one enabled file mailbox")
	}
	m, err := adapter.OpenFileMailbox(listener.Address)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, m.Close()) }()
	v, err := auth.New(c)
	if err != nil {
		return err
	}
	b, err := backend.New(c)
	if err != nil {
		return err
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, c)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	d, err := dispatch.New(s, v, b, cursors, listener.Operations)
	if err != nil {
		return err
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { err := runner.Run(workCtx); cancel(); done <- err }()
	err = adapter.ProcessFileMailbox(workCtx, m, d)
	if err == nil && workCtx.Err() == nil {
		grace, stop := context.WithTimeout(workCtx, 5*time.Second)
		for grace.Err() == nil {
			pending, pendingErr := s.HasPending(grace)
			if pendingErr != nil {
				if grace.Err() == nil {
					err = pendingErr
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
		stop()
	}
	cancel()
	return errors.Join(err, <-done)
}
