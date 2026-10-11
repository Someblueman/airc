package backend

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestReadPoolNeverConsumesSoleSendSlot(t *testing.T) {
	for _, limits := range [][2]int{{1, 1}, {4, 1}, {1, 2}} {
		t.Run(fmt.Sprint(limits), func(t *testing.T) {
			b := &Backend{active: map[string]int{}, lookupActive: map[string]int{}, globalLimit: limits[0], authorLimit: limits[1]}
			if b.acquire("one", store.LookupAttempt) || b.total != 0 || b.lookupTotal != 0 || b.active["one"] != 0 || b.lookupActive["one"] != 0 {
				t.Fatal("read consumed reserved send capacity", b)
			}
			if !b.acquire("one", store.SendAttempt) {
				t.Fatal("send reserve unavailable")
			}
			b.release("one", store.SendAttempt)
			if !b.acquire("one", store.SendAttempt) {
				t.Fatal("released reserve unavailable")
			}
			b.release("one", store.SendAttempt)
		})
	}
}

func TestSessionCapsAndSendReserve(t *testing.T) {
	b := &Backend{active: map[string]int{}, lookupActive: map[string]int{}, globalLimit: 4, authorLimit: 2}
	if !b.acquire("one", store.LookupAttempt) || b.acquire("one", store.LookupAttempt) {
		t.Fatal("lookup consumed author send reserve")
	}
	if !b.acquire("one", store.SendAttempt) || b.acquire("one", store.SendAttempt) {
		t.Fatal("author cap failed")
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
