//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

type directoryMailbox struct {
	mailbox    *adapter.DirectoryMailbox
	dispatcher *dispatch.Dispatcher
	requests   chan struct{}
	// bound is the author of requests that name none.
	bound *dispatch.BoundAuthor
}

func openDirectoryMailbox(c config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (mailboxOwner, error) {
	bound, err := loadBoundAuthor(l.BoundAuthor)
	if err != nil {
		return nil, err
	}
	m, err := adapter.OpenDirectoryMailbox(l.Address)
	if err != nil {
		return nil, err
	}
	return &directoryMailbox{mailbox: m, dispatcher: d, requests: requests, bound: bound}, nil
}

func (m *directoryMailbox) Close() error { return m.mailbox.Close() }

func (m *directoryMailbox) serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case m.requests <- struct{}{}:
		}
		err := adapter.ProcessDirectoryMailbox(ctx, m.mailbox, m.dispatcher, m.bound)
		<-m.requests
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, adapter.ErrMailboxResultPending) {
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
