//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestIPv4MulticastServiceStartupAndProtectedCapabilities(t *testing.T) {
	f := newIPv4MulticastFixture(t)
	if f.address != f.config.Listeners[0].Address {
		t.Fatal("ready exposed a wildcard bind instead of the configured group", f.address)
	}
	checkDatagramReadOnlyCLI(t, f.process, f.config, "ipv4_multicast")
	response := udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "mcast-startup", Operation: core.CapabilitiesGet}, true)
	if response.Result.Capabilities == nil {
		t.Fatal("missing protected capabilities")
	}
	assertUDPSilent(t, f.source)
	rpcNoJobs(t, f.config.StateDir)
}
