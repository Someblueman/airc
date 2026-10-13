//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
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
	listener, ok := soleListener(c, "file_mailbox")
	if !ok {
		return errors.New("mailbox-once requires exactly one enabled file mailbox")
	}
	m, err := adapter.OpenFileMailbox(listener.Address)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, m.Close()) }()
	s, b, d, err := openListenerDispatch(ctx, c, listener)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	bound, err := loadBoundAuthor(listener.BoundAuthor)
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
	err = adapter.ProcessFileMailbox(workCtx, m, d, bound)
	if err == nil && workCtx.Err() == nil {
		grace, stop := context.WithTimeout(workCtx, 5*time.Second)
		err = waitDelivered(grace, s)
		stop()
	}
	cancel()
	return errors.Join(err, <-done)
}
