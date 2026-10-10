//go:build darwin || linux

package gateway_test

import (
	"net"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestIPv6MulticastServiceStartupAndProtectedCapabilities(t *testing.T) {
	f := newIPv6MulticastFixture(t)
	ready, err := net.ResolveUDPAddr("udp6", f.address)
	configured, errConfigured := net.ResolveUDPAddr("udp6", f.config.Listeners[0].Address)
	if err != nil || errConfigured != nil || !ready.IP.Equal(configured.IP) || ready.Port != configured.Port || ready.Zone != f.config.Listeners[0].IPv6Multicast.Interface {
		t.Fatal("ready did not expose the configured multicast group and interface", f.address, err, errConfigured)
	}
	checkDatagramReadOnlyCLI(t, f.process, f.config, "ipv6_multicast")
	response := udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "mcast6-startup", Operation: core.CapabilitiesGet}, true)
	if response.Result.Capabilities == nil {
		t.Fatal("missing protected capabilities")
	}
	assertUDPSilent(t, f.source)
	rpcNoJobs(t, f.config.StateDir)
}
