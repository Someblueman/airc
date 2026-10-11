//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// With one gateway session slot, a second pinned syslog socket is refused
// while the first holds it; the gateway stops with that socket open and
// releases the listener's address.
func TestSyslogNativeSharedSocketAdmissionAndJoinedShutdown(t *testing.T) {
	e := newRouteEnv(t, routeCarriers[0], func(c *config.Config) {
		c.Limits.Sessions, c.Limits.SessionsPerAuthor = 1, 1
		c.Limits.Watches, c.Limits.WatchesPerAuthor = 0, 0
		c.Limits.Workers = 1
	})
	p := e.provider.(*syslogRoute)
	peer := p.connect(t)
	item := peer.deliver(t, e.text("single slot"))
	seen := e.observed.wait(t, item.text)
	if seen.From != "alice" || seen.Target != routeRoom {
		t.Fatal(seen)
	}
	if rejected, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", e.address, p.trusted()); err == nil {
		_ = rejected.NetConn().Close()
		t.Fatal("second socket bypassed shared admission")
	}
	// The receipts listener shares the one slot, so read acceptance from the
	// journal itself.
	for deadline := time.Now().Add(10 * time.Second); e.jobState(t, item.id) != core.Accepted; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("job not accepted", e.jobState(t, item.id))
		}
	}
	e.gateway.stop(t)
	peer.close()
	listener, err := net.Listen("tcp", e.address)
	if err != nil {
		t.Fatal("listener retained after shutdown", err)
	}
	_ = listener.Close()
	if messages := e.messages(t); len(messages) != 1 || messages[0].ID != seen.ID {
		t.Fatal(messages)
	}
}
