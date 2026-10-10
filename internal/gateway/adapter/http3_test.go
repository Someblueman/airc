package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestHTTP3ServerRejectsInvalidConstruction(t *testing.T) {
	if _, err := HTTP3Server(nil, 1, nil); err != store.ErrInvalid {
		t.Fatalf("nil dispatcher err=%v", err)
	}
	if _, err := HTTP3Server(nil, 0, nil); err != store.ErrInvalid {
		t.Fatalf("zero slots err=%v", err)
	}
	if _, err := HTTP3Server(nil, 257, nil); err != store.ErrInvalid {
		t.Fatalf("oversized slots err=%v", err)
	}
}
