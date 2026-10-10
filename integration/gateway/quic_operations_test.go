//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRawQUICBidiNativeSendAndReads(t *testing.T) {
	f := newRawQUICFixture(t, false)
	exerciseCarrierSendAndReads(t, func(r core.Request, encrypted bool) adapter.FrameResponse { return rawQUICBidiCall(t, f, r, encrypted) }, func(r core.Request, encrypted bool) core.Result { return rawQUICAccepted(t, f, r, encrypted) }, nativeOperationFixture{config: f.config, observer: f.observer}, "quic-bidi")
}

func TestRawQUICDatagramNativeSendAndReads(t *testing.T) {
	f := newRawQUICFixture(t, true)
	exerciseCarrierSendAndReads(t, func(r core.Request, encrypted bool) adapter.FrameResponse {
		return rawQUICDatagramCall(t, f, r, encrypted)
	}, func(r core.Request, encrypted bool) core.Result { return rawQUICDatagramAccepted(t, f, r, encrypted) }, nativeOperationFixture{config: f.config, observer: f.observer}, "quic-datagram")
}

func TestRawQUICUniNativeSendAndReads(t *testing.T) {
	f := newRawQUICFixture(t, false)
	exerciseCarrierSendAndReads(t, func(r core.Request, encrypted bool) adapter.FrameResponse {
		return rawQUICUniCall(t, f, r, encrypted)
	}, func(r core.Request, encrypted bool) core.Result {
		return rawQUICUniAccepted(t, f, r, encrypted)
	}, nativeOperationFixture{config: f.config, observer: f.observer}, "quic-uni")
}
