//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCoAPSTCPNativeSendAndReads(t *testing.T) {
	for _, mode := range []struct {
		label     string
		encrypted bool
	}{
		{label: "coaps_tcp-signed"},
		{label: "coaps_tcp-encrypted", encrypted: true},
	} {
		t.Run(mode.label, func(t *testing.T) {
			f := newCoAPTCPFixture(t)
			call := func(request core.Request, _ bool) adapter.FrameResponse {
				return coapTCPRPC(t, f, request, mode.encrypted)
			}
			accepted := func(request core.Request, _ bool) core.Result {
				return coapTCPAccepted(t, f, request, mode.encrypted)
			}
			exerciseCarrierSendAndReads(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, mode.label)
		})
	}
}
