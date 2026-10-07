//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"github.com/Someblueman/airc/pkg/irc"
	"net"
	"testing"
	"time"
)

func TestSyslogNativeSharedSocketAdmissionAndJoinedShutdown(t *testing.T) {
	f := prepareSyslogFixture(t)
	f.config.Limits.Sessions, f.config.Limits.SessionsPerPrincipal = 1, 1
	f.config.Limits.Watches, f.config.Limits.WatchesPerPrincipal = 0, 0
	f.config.Limits.Workers = 1
	f.start(t)
	peer := f.connect(t)
	raw := syslogNativeRecord("capacity", "syslog single slot body")
	syslogNativeWrite(t, peer, raw)
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "syslog single slot body" })
	if event.From != "fixture-author" || event.Target != "#gateway-fixture" {
		t.Fatal(event)
	}
	c := f.cert.Client.Clone()
	c.ServerName = "localhost"
	c.Certificates = []tls.Certificate{f.client}
	rejected, e := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", f.address, c)
	if e == nil {
		rejected.NetConn().Close()
		t.Fatal("second socket bypassed direct shared admission")
	}
	peer.NetConn().Close()
	f.accepted(t, raw, event.ID)
	f.stop(t)
	listener, e := net.Listen("tcp", f.address)
	if e != nil {
		t.Fatal("listener owner retained after shutdown", e)
	}
	listener.Close()
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 1 || messages[0].ID != event.ID {
		t.Fatal(messages)
	}
}
