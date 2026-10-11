package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestDTLSDatagramCarrierRejectsOversizedPayloadBeforeConnUse(t *testing.T) {
	c := &dtlsDatagramCarrier{}
	err := c.SendDatagram(make([]byte, fragmentDatagramBytes+1))
	if !errors.Is(err, store.ErrCapacity) {
		t.Fatalf("error = %v, want %v", err, store.ErrCapacity)
	}
}
