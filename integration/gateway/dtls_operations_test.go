//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestDTLSAllSharedOperations(t *testing.T) {
	f := newDTLSFixture(t)
	call := func(request core.Request, encrypted bool) adapter.FrameResponse {
		return dtlsDatagramCall(t, f, request, encrypted)
	}
	accepted := func(request core.Request, encrypted bool) core.Result {
		return dtlsDatagramAccepted(t, f, request, encrypted)
	}
	exerciseNativeOperations(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, "dtls-datagram")
}
