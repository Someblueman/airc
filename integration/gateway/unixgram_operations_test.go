//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUnixgramAllSharedOperations(t *testing.T) {
	f := newUnixgramFixture(t)
	checkDatagramReadOnlyCLI(t, f.process, f.config, "unixgram")
	call := func(request core.Request, encrypted bool) adapter.FrameResponse {
		return unixgramCall(t, f, request, encrypted)
	}
	accepted := func(request core.Request, encrypted bool) core.Result {
		return unixgramAccepted(t, f, request, encrypted)
	}
	exerciseNativeOperations(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, "unixgram")
}
