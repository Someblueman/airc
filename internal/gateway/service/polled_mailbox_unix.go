//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// polledMailbox serves a file-system mailbox one input at a time, polling at
// a fixed interval while process reports nothing to do (idle).
type polledMailbox struct {
	process  func(context.Context) error
	idle     func(error) bool
	close    func() error
	requests chan struct{}
}

func openDirectoryMailbox(_ config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (mailboxOwner, error) {
	bound, err := loadBoundAuthor(l.BoundAuthor)
	if err != nil {
		return nil, err
	}
	m, err := adapter.OpenDirectoryMailbox(l.Address)
	if err != nil {
		return nil, err
	}
	return &polledMailbox{
		process: func(ctx context.Context) error { return adapter.ProcessDirectoryMailbox(ctx, m, d, bound) },
		idle: func(err error) bool {
			return errors.Is(err, os.ErrNotExist) || errors.Is(err, adapter.ErrMailboxResultPending)
		},
		close:    m.Close,
		requests: requests,
	}, nil
}

func openAppendLog(_ config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (mailboxOwner, error) {
	bound, err := loadBoundAuthor(l.BoundAuthor)
	if err != nil {
		return nil, err
	}
	m, err := adapter.OpenAppendLogMailbox(l.Address)
	if err != nil {
		return nil, err
	}
	return &polledMailbox{
		process: func(ctx context.Context) error { return adapter.ProcessAppendLogMailbox(ctx, m, d, bound) },
		idle: func(err error) bool {
			return errors.Is(err, io.EOF) || errors.Is(err, adapter.ErrAppendLogIncomplete) || errors.Is(err, adapter.ErrMailboxResultPending)
		},
		close:    m.Close,
		requests: requests,
	}, nil
}

func (m *polledMailbox) Close() error { return m.close() }

func (m *polledMailbox) serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case m.requests <- struct{}{}:
		}
		err := m.process(ctx)
		<-m.requests
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !m.idle(err) {
			return err
		}
		// A fixed idle interval bounds polling without retaining queue contents.
		if err != nil {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
}
