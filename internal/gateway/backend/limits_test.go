package backend

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSessionCapsAndSendReserve(t *testing.T) {
	b := &Backend{active: map[string]int{}, lookupActive: map[string]int{}, globalLimit: 4, principalLimit: 2}
	if !b.acquire("one", store.LookupAttempt) || b.acquire("one", store.LookupAttempt) {
		t.Fatal("lookup consumed principal send reserve")
	}
	if !b.acquire("one", store.SendAttempt) || b.acquire("one", store.SendAttempt) {
		t.Fatal("principal cap failed")
	}
	if !b.acquire("two", store.LookupAttempt) || !b.acquire("three", store.SendAttempt) || b.acquire("four", store.SendAttempt) {
		t.Fatal("global cap failed")
	}
	b.release("three", store.SendAttempt)
	if !b.acquire("four", store.SendAttempt) {
		t.Fatal("closed session did not release capacity")
	}
	b.release("one", store.SendAttempt)
	b.release("one", store.LookupAttempt)
	b.release("two", store.LookupAttempt)
	b.release("four", store.SendAttempt)
	if b.total != 0 || b.lookupTotal != 0 {
		t.Fatal("session accounting leaked")
	}
}
