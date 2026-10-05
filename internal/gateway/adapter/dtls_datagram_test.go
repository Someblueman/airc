package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/pion/dtls/v3"
)

func TestDTLSALPN(t *testing.T) {
	if DTLSALPN != "airc-gateway-dtls/1" {
		t.Fatalf("DTLSALPN = %q", DTLSALPN)
	}
}

func TestServeDTLSDatagramsRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	requests := make(chan struct{}, 1)
	if err := ServeDTLSDatagrams(ctx, (*dtls.Conn)(nil), nil, requests, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("nil connection error = %v, want %v", err, store.ErrInvalid)
	}
	if err := ServeDTLSDatagrams(ctx, nil, nil, requests, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("nil dispatcher error = %v, want %v", err, store.ErrInvalid)
	}
	if err := ServeDTLSDatagrams(ctx, nil, &dispatch.Dispatcher{}, make(chan struct{}), nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("empty requests error = %v, want %v", err, store.ErrInvalid)
	}
}

func TestDTLSDatagramCarrierRejectsOversizedPayloadBeforeConnUse(t *testing.T) {
	c := &dtlsDatagramCarrier{}
	err := c.SendDatagram(make([]byte, agd1DatagramBytes+1))
	if !errors.Is(err, store.ErrCapacity) {
		t.Fatalf("error = %v, want %v", err, store.ErrCapacity)
	}
}
