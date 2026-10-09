//go:build darwin || linux

package adapter

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSCTPApplicationCapacityIsProtectedBeforeIntake(t *testing.T) {
	f, request, protection, open := sctpApplicationFixture(t)
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	client, cancel, done := startSCTPApplication(t, f, request, protection, slots, nil, SCTPOrdered)
	buffer := make([]byte, auth.MaxResponseTokenBytes)
	n, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	response := open(buffer[:n])
	if response.Event != "error" || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient || response.Sequence != 1 || response.CorrelationID != request.CorrelationID {
		t.Fatalf("unprotected or uncorrelated capacity response: %+v", response)
	}
	if _, err := f.s.Lookup(t.Context(), f.identity, amqpRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("capacity request reached intake: %v", err)
	}
	cancel()
	awaitSCTPApplication(t, done)
}

func TestSCTPApplicationMissingRouteAndSignedOnlyCloseWithoutIntake(t *testing.T) {
	for _, mode := range []string{"missing-route", "signed-only"} {
		t.Run(mode, func(t *testing.T) {
			f, request, protection, _ := sctpApplicationFixture(t)
			if mode == "missing-route" {
				clear(protection.routes)
			} else {
				request.Envelope = string(redisSignedRequest(t, f, amqpRequest(), 0))
			}
			client, _, done := startSCTPApplication(t, f, request, protection, make(chan struct{}, 1), nil, SCTPOrdered)
			if n, err := client.Read(make([]byte, auth.MaxResponseTokenBytes)); n != 0 || err == nil {
				t.Fatalf("refused request got a response: n=%d err=%v", n, err)
			}
			awaitSCTPApplication(t, done)
			if _, err := f.s.Lookup(t.Context(), f.identity, amqpRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("refused request reached intake: %v", err)
			}
		})
	}
}

func TestSCTPApplicationCancelJoinsBlockedResponseAndReleasesCapacity(t *testing.T) {
	f, request, protection, _ := sctpApplicationFixture(t)
	slots := make(chan struct{}, 1)
	writing := make(chan struct{})
	_, cancel, done := startSCTPApplication(t, f, request, protection, slots, writing, SCTPOrdered)
	select {
	case <-writing:
	case <-time.After(5 * time.Second):
		t.Fatal("response never reached the blocked writer")
	}
	cancel()
	awaitSCTPApplication(t, done)
	if len(slots) != 0 {
		t.Fatal("cancelled writer retained the request slot")
	}
}

func TestSCTPUnorderedApplicationRejectsUnboundCorrelationBeforeIntake(t *testing.T) {
	f, request, protection, _ := sctpApplicationFixture(t)
	client, _, done := startSCTPApplication(t, f, request, protection, make(chan struct{}, 1), nil, SCTPUnordered)
	if n, err := client.Read(make([]byte, auth.MaxResponseTokenBytes)); n != 0 || err == nil {
		t.Fatalf("unbound request got a response: n=%d err=%v", n, err)
	}
	awaitSCTPApplication(t, done)
	if _, err := f.s.Lookup(t.Context(), f.identity, amqpRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unbound request reached intake: %v", err)
	}
}

func awaitSCTPApplication(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SCTP application did not join")
	}
}
