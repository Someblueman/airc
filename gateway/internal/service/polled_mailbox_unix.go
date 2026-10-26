//go:build darwin || linux

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

// polledMailbox serves a file-system mailbox one input at a time, polling at
// a fixed interval while process reports nothing to do (idle).
type polledMailbox struct {
	id       string
	process  func(context.Context) error
	idle     func(error) bool
	close    func() error
	requests chan struct{}
	passes   uint64 // completed passes over the mailbox, idle or not
}

// mailboxPass is one completed pass of a polled mailbox: Idle says why the
// pass found nothing to dispatch (an incomplete record, a full results
// directory, an empty inbox), or is empty after a dispatched input.
type mailboxPass struct {
	Listener string    `json:"listener"`
	Pass     uint64    `json:"pass"`
	Began    time.Time `json:"began"`
	Idle     string    `json:"idle,omitempty"`
}

// mailboxPassed observes every completed pass of a polled mailbox, so a test
// can wait for the poller to have looked at an input instead of for a fixed
// time. It does nothing unless AIRC_GATEWAY_MAILBOX_TRACE is set, when each
// pass goes to stderr as one JSON line for a parent process to read. Tests in
// this package replace it before starting the service.
var mailboxPassed = mailboxTraceFromEnv()

func mailboxTraceFromEnv() func(mailboxPass) {
	if os.Getenv("AIRC_GATEWAY_MAILBOX_TRACE") == "" {
		return func(mailboxPass) {}
	}
	encoder := json.NewEncoder(os.Stderr)
	return func(p mailboxPass) { _ = encoder.Encode(p) }
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
		id:      l.ID,
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
		id:      l.ID,
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
		began := time.Now()
		err := m.process(ctx)
		<-m.requests
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !m.idle(err) {
			return err
		}
		m.passes++
		pass := mailboxPass{Listener: m.id, Pass: m.passes, Began: began}
		if err != nil {
			pass.Idle = err.Error()
		}
		mailboxPassed(pass)
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
