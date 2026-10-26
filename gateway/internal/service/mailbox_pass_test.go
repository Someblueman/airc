//go:build darwin || linux

package service

import (
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testwait"
)

// mailboxPasses records every polled mailbox pass of the services a test
// starts after observeMailboxPasses, through the mailboxPassed hook.
type mailboxPasses struct {
	mu     sync.Mutex
	passes []mailboxPass
}

// observeMailboxPasses installs the recording hook for the rest of the test.
// It is called before the service starts, and the hook is restored only after
// the test's own cleanups have joined the service.
func observeMailboxPasses(t *testing.T) *mailboxPasses {
	t.Helper()
	o := &mailboxPasses{}
	previous := mailboxPassed
	mailboxPassed = func(p mailboxPass) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.passes = append(o.passes, p)
	}
	t.Cleanup(func() { mailboxPassed = previous })
	return o
}

// after waits for a pass that began after since, so one that saw the state
// the test had put in place by then, and returns it.
func (o *mailboxPasses) after(t *testing.T, since time.Time, what string) mailboxPass {
	t.Helper()
	var found mailboxPass
	testwait.Every(t, 5*time.Second, 10*time.Millisecond, what, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		for _, p := range o.passes {
			if p.Began.After(since) {
				found = p
				return true
			}
		}
		return false
	})
	return found
}
