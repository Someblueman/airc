package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/pion/dtls/v3"
)

// TestCarrierServersRefuseMissingDependencies: no carrier server is built,
// or served, without a dispatcher and a usable request gate.
func TestCarrierServersRefuseMissingDependencies(t *testing.T) {
	gate := make(chan struct{}, 1)
	ctx := context.Background()
	for name, construct := range map[string]func() error{
		"http3/nil-dispatcher":           func() error { _, err := HTTP3Server(nil, 1, nil); return err },
		"http3/zero-slots":               func() error { _, err := HTTP3Server(nil, 0, nil); return err },
		"http3/oversized-slots":          func() error { _, err := HTTP3Server(nil, 257, nil); return err },
		"quic/nil-dispatcher":            func() error { _, err := QUICServer(nil, gate, false); return err },
		"quic/nil-gate":                  func() error { _, err := QUICServer(nil, nil, true); return err },
		"webtransport/nil-dispatcher":    func() error { _, err := WebTransportServer(nil, gate, nil); return err },
		"webtransport/nil-gate":          func() error { _, err := WebTransportServer(nil, nil, nil); return err },
		"grpc/nil-dispatcher":            func() error { _, err := GRPCServer(nil, gate); return err },
		"grpc/nil-gate":                  func() error { _, err := GRPCServer(nil, nil); return err },
		"dtls-datagrams/nil-connection":  func() error { return ServeDTLSDatagrams(ctx, (*dtls.Conn)(nil), nil, gate, nil) },
		"dtls-datagrams/nil-dispatcher":  func() error { return ServeDTLSDatagrams(ctx, nil, nil, gate, nil) },
		"dtls-datagrams/unbuffered-gate": func() error { return ServeDTLSDatagrams(ctx, nil, &dispatch.Dispatcher{}, make(chan struct{}), nil) },
	} {
		if err := construct(); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err=%v, want %v", name, err, store.ErrInvalid)
		}
	}
	for alpn, want := range map[string]string{QUICALPN: "airc-gateway-quic/1", DTLSALPN: "airc-gateway-dtls/1"} {
		if alpn != want {
			t.Errorf("ALPN %q, want %q", alpn, want)
		}
	}
}
