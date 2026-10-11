//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

type appendLog struct {
	mailbox            *adapter.AppendLogMailbox
	dispatcher         *dispatch.Dispatcher
	principal, binding string
	requests           chan struct{}
	// bound is the plain author default when principal is empty.
	bound *dispatch.BoundAuthor
}

func openAppendLog(c config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (mailboxOwner, error) {
	if c.LocalAuthorMode(l) {
		bound, err := loadBoundAuthor(l.BoundAuthor)
		if err != nil {
			return nil, err
		}
		m, err := adapter.OpenAppendLogMailbox(l.Address)
		if err != nil {
			return nil, err
		}
		return &appendLog{mailbox: m, dispatcher: d, requests: requests, bound: bound}, nil
	}
	for _, p := range c.Principals {
		if l.AppendLog != nil && p.ID == l.AppendLog.Principal {
			m, err := adapter.OpenAppendLogMailbox(l.Address)
			if err != nil {
				return nil, err
			}
			return &appendLog{mailbox: m, dispatcher: d, principal: p.ID, binding: p.Binding, requests: requests}, nil
		}
	}
	return nil, errors.New("append log owner is unknown")
}

func (m *appendLog) Close() error { return m.mailbox.Close() }

func (m *appendLog) serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case m.requests <- struct{}{}:
		}
		var err error
		if m.principal == "" {
			err = adapter.ProcessAppendLogMailboxAuthor(ctx, m.mailbox, m.dispatcher, m.bound)
		} else {
			err = adapter.ProcessAppendLogMailbox(ctx, m.mailbox, m.dispatcher, m.principal, m.binding)
		}
		<-m.requests
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, adapter.ErrAppendLogIncomplete) && !errors.Is(err, adapter.ErrMailboxResultPending) {
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
