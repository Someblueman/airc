package auth

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
)

func TestResponseSealerEnforcesReceiverEnvelopeDepth(t *testing.T) {
	m := newBoundaryMaterial(t)
	sealer, err := NewResponseSealer("signer", m.signing)
	if err != nil {
		t.Fatal(err)
	}
	for _, depth := range []int{7, 8} {
		payload := []byte(strings.Repeat(`{"x":`, depth) + "0" + strings.Repeat("}", depth))
		token, err := sealer.Seal(m.scope, "recipient", &m.recipient.PublicKey, payload, m.now)
		if depth == 8 {
			if !errors.Is(err, ErrResponseCapacity) {
				t.Fatalf("unreadable envelope emitted: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := OpenResponse(token, "signer", m.signing.Public().(ed25519.PublicKey), "recipient", m.recipient, m.scope, m.now)
		if err != nil || string(got) != string(payload) {
			t.Fatalf("boundary envelope failed: %v", err)
		}
	}
}
