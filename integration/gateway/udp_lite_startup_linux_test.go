//go:build linux

package gateway_test

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"testing"
)

func TestUDPLiteServiceStartupAndProtectedCapabilities(t *testing.T) {
	f := newUDPLiteFixture(t)
	checkDatagramReadOnlyCLI(t, f.process, f.config, "udp_lite")
	response := udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "udp-lite-startup", Operation: core.CapabilitiesGet}, true)
	if response.Result.Capabilities == nil {
		t.Fatal("missing protected UDP-Lite capabilities")
	}
	assertUDPSilent(t, f.source)
	rpcNoJobs(t, f.config.StateDir)
}
