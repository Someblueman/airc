package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// MaxRequests is the most requests Requests allows in flight: the most
// sessions plus the most waiting checks.
const MaxRequests = 1024 + 4096

// Requests is how many requests may be in flight at once across the
// listeners: those using an upstream session, and checks waiting for news.
func (l Limits) Requests() int { return l.Sessions + l.Waits }

func (c Config) validateLimits() error {
	l, r := c.Limits, c.Retry
	if l.Workers < 1 || l.Workers > 64 || l.Connections < 1 || l.Connections > 16384 || l.Sessions < 1 || l.Sessions > 1024 || l.Workers > l.Sessions || l.SessionsPerAuthor < 1 || l.SessionsPerAuthor > l.Sessions || l.IdleSessions < 0 || l.IdleSessions > 4096 || l.Waits < 0 || l.Waits > 4096 || l.Watches < 0 || l.Watches >= l.Sessions || l.WatchesPerAuthor < 0 || l.WatchesPerAuthor >= l.SessionsPerAuthor || l.WatchesPerAuthor > l.Watches || l.Pending < 1 || l.Pending > 65536 || l.PendingPerAuthor < 1 || l.PendingPerAuthor > l.Pending || l.JournalBytes < 1<<20 || l.JournalBytes > 1<<30 {
		return errors.New("invalid resource limits or missing send reserve")
	}
	if r.Policy != core.Retry && r.Policy != core.ReceiptOnly || r.SendAttempts < 1 || r.SendAttempts > 100 || r.LookupAttempts < 1 || r.LookupAttempts > 100 || r.LifetimeSeconds < 1 || r.LifetimeSeconds > 30*86400 || r.BackoffSeconds < 1 || r.MaxBackoffSeconds < r.BackoffSeconds || r.MaxBackoffSeconds > 3600 || r.TerminalRetentionSeconds < r.LifetimeSeconds || r.TerminalRetentionSeconds > 90*86400 {
		return errors.New("invalid retry or retention limits")
	}
	return nil
}
