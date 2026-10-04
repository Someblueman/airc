//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestFramedCLINativeIPv6(t *testing.T) {
	for _, secure := range []bool{false, true} {
		conn, credentials, stop := framedFixtureAddress(t, secure, "[::1]:0")
		profile := "encrypted"
		if secure {
			profile = "signed"
		}
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "ipv6", Profile: profile, Envelope: credentials.token(t, core.Request{Version: 1, RequestID: "ipv6", Operation: core.CapabilitiesGet}, !secure)})
		if r := readFrame(t, conn); r.Result == nil || r.Result.Capabilities == nil {
			t.Fatal("native IPv6 frame failed", r)
		}
		stop()
	}
}
