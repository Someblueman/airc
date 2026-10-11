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
	mailbox            *adapter.DirectoryMailbox
	dispatcher         *dispatch.Dispatcher
	principal, binding string
	requests           chan struct{}
	// bound is the plain author default when principal is empty.
	bound *dispatch.BoundAuthor
}

func openDirectoryMailbox(c config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (mailboxOwner, error) {
	if c.LocalAuthorMode(l) {
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
	for _, p := range c.Principals {
		if l.DirectoryMailbox != nil && p.ID == l.DirectoryMailbox.Principal {
			m, err := adapter.OpenDirectoryMailbox(l.Address)
			if err != nil {
				return nil, err
			}
			return &directoryMailbox{mailbox: m, dispatcher: d, principal: p.ID, binding: p.Binding, requests: requests}, nil
		}
	}
	return nil, errors.New("directory mailbox owner is unknown")
}

func (m *directoryMailbox) Close() error { return m.mailbox.Close() }

func (m *directoryMailbox) serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case m.requests <- struct{}{}:
		}
		var err error
		if m.principal == "" {
			err = adapter.ProcessDirectoryMailboxAuthor(ctx, m.mailbox, m.dispatcher, m.bound)
		} else {
			err = adapter.ProcessDirectoryMailbox(ctx, m.mailbox, m.dispatcher, m.principal, m.binding)
		}
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
