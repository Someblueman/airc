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
}

func openDirectoryMailbox(c config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (directoryMailboxOwner, error) {
	for _, p := range c.Principals {
		if l.DirectoryMailbox != nil && p.ID == l.DirectoryMailbox.Principal {
			m, err := adapter.OpenDirectoryMailbox(l.Address)
			if err != nil {
				return nil, err
			}
			return &directoryMailbox{m, d, p.ID, p.Binding, requests}, nil
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
		err := adapter.ProcessDirectoryMailbox(ctx, m.mailbox, m.dispatcher, m.principal, m.binding)
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
