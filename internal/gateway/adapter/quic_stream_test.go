package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestQUICServerConstructionAndALPN(t *testing.T) {
	if QUICALPN != "airc-gateway-quic/1" {
		t.Fatalf("unexpected ALPN %q", QUICALPN)
	}
	if _, err := QUICServer(nil, make(chan struct{}, 1), false); err != store.ErrInvalid {
		t.Fatalf("nil dispatcher err=%v", err)
	}
	if _, err := QUICServer(nil, nil, true); err != store.ErrInvalid {
		t.Fatalf("nil request gate err=%v", err)
	}
}
