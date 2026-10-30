//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestUnixgramNativeSendAndReads(t *testing.T) {
	t.Parallel()
	f := newUnixgramFixture(t)
	checkDatagramReadOnlyCLI(t, f.process, f.config, "unixgram")
	call := func(request core.Request, encrypted bool) adapter.FrameResponse {
		return unixgramCall(t, f, request, encrypted)
	}
	accepted := func(request core.Request, encrypted bool) core.Result {
		return unixgramAccepted(t, f, request, encrypted)
	}
	exerciseCarrierSendAndReads(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, "unixgram")
}
