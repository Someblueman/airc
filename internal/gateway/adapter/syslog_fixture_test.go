//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// syslogTestAuthor is the bound author of the fixture's sender grant.
const syslogTestAuthor = "syslog-host"

type syslogAuthorFixture struct {
	*routeFixture
	d      *dispatch.Dispatcher
	author auth.RouteAuthor
	target core.Target
}

func newSyslogAuthorFixture(t *testing.T) syslogAuthorFixture {
	t.Helper()
	f := newRouteFixture(t)
	target := core.Target{Kind: "room", Name: "#fixture"}
	d := f.listener(t, config.Listener{ID: "syslog", Protocol: "syslog_tls", Operations: []core.Operation{core.Send}})
	return syslogAuthorFixture{routeFixture: f, d: d, author: auth.RouteAuthor{Author: core.Author{Nick: syslogTestAuthor}}, target: target}
}

func (f syslogAuthorFixture) lookup(t *testing.T, requestID string) (store.Job, error) {
	t.Helper()
	return f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: syslogTestAuthor}}, requestID)
}
