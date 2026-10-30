package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/store"
)

// TestCarrierServersRefuseMissingDependencies: no carrier server is built,
// or served, without a dispatcher and a usable request gate.
func TestCarrierServersRefuseMissingDependencies(t *testing.T) {
	gate := make(chan struct{}, 1)
	for name, construct := range map[string]func() error{
		"http3/nil-dispatcher":        func() error { _, err := HTTP3Server(nil, 1, nil); return err },
		"http3/zero-slots":            func() error { _, err := HTTP3Server(nil, 0, nil); return err },
		"http3/oversized-slots":       func() error { _, err := HTTP3Server(nil, 257, nil); return err },
		"quic/nil-dispatcher":         func() error { _, err := QUICServer(nil, gate, false); return err },
		"quic/nil-gate":               func() error { _, err := QUICServer(nil, nil, true); return err },
		"webtransport/nil-dispatcher": func() error { _, err := WebTransportServer(nil, gate, nil); return err },
		"webtransport/nil-gate":       func() error { _, err := WebTransportServer(nil, nil, nil); return err },
		"grpc/nil-dispatcher":         func() error { _, err := GRPCServer(nil, gate); return err },
		"grpc/nil-gate":               func() error { _, err := GRPCServer(nil, nil); return err },
	} {
		if err := construct(); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err=%v, want %v", name, err, store.ErrInvalid)
		}
	}
	if QUICALPN != "airc-gateway-quic/1" {
		t.Errorf("ALPN %q, want %q", QUICALPN, "airc-gateway-quic/1")
	}
}
