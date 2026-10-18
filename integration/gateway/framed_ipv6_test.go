//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testnet"
)

func TestFramedCLINativeIPv6(t *testing.T) {
	testnet.RequireIPv6Loopback(t)
	for _, secure := range []bool{false, true} {
		conn, credentials, _, stop := framedFixtureAddress(t, secure, "[::1]:0")
		profile := "encrypted"
		if secure {
			profile = "signed"
		}
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "ipv6", Profile: profile, Request: credentials.token(t, core.Request{Version: 1, RequestID: "ipv6", Operation: core.CapabilitiesGet}, !secure)})
		if r := readFrame(t, conn); r.Result == nil || r.Result.Capabilities == nil {
			t.Fatal("native IPv6 frame failed", r)
		}
		stop()
	}
}
