//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSCTPApplicationCapacityIsProtectedBeforeIntake(t *testing.T) {
	f, request := sctpApplicationFixture(t)
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	client, cancel, done := startSCTPApplication(t, f, request, slots, nil, SCTPOrdered)
	buffer := make([]byte, MaxResponseFrameBytes)
	n, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	var response FrameResponse
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		t.Fatal(err)
	}
	if response.Event != "error" || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient || response.Sequence != 1 || response.CorrelationID != request.CorrelationID {
		t.Fatalf("uncorrelated capacity response: %+v", response)
	}
	if _, err := f.lookup(t, mqttSNRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("capacity request reached intake: %v", err)
	}
	cancel()
	awaitSCTPApplication(t, done)
}

func TestSCTPApplicationUnknownProfileClosesWithoutIntake(t *testing.T) {
	for _, mode := range []string{"profile", "missing-request"} {
		t.Run(mode, func(t *testing.T) {
			f, request := sctpApplicationFixture(t)
			if mode == "profile" {
				request.Profile = "encrypted"
			} else {
				request.Request = nil
			}
			client, _, done := startSCTPApplication(t, f, request, make(chan struct{}, 1), nil, SCTPOrdered)
			if n, err := client.Read(make([]byte, MaxResponseFrameBytes)); n != 0 || err == nil {
				t.Fatalf("refused request got a response: n=%d err=%v", n, err)
			}
			awaitSCTPApplication(t, done)
			if _, err := f.lookup(t, mqttSNRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("refused request reached intake: %v", err)
			}
		})
	}
}

func TestSCTPApplicationCancelJoinsBlockedResponseAndReleasesCapacity(t *testing.T) {
	f, request := sctpApplicationFixture(t)
	slots := make(chan struct{}, 1)
	writing := make(chan struct{})
	_, cancel, done := startSCTPApplication(t, f, request, slots, writing, SCTPOrdered)
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

func TestSCTPApplicationRejectsInvalidCorrelationBeforeIntake(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode SCTPMode
	}{{"ordered", SCTPOrdered}, {"unordered", SCTPUnordered}, {"ordered ttl", SCTPOrderedTTL}} {
		t.Run(tc.name, func(t *testing.T) {
			f, request := sctpApplicationFixture(t)
			request.CorrelationID = "not a correlation id"
			client, _, done := startSCTPApplication(t, f, request, make(chan struct{}, 1), nil, tc.mode)
			if n, err := client.Read(make([]byte, MaxResponseFrameBytes)); n != 0 || err == nil {
				t.Fatalf("invalid request got a response: n=%d err=%v", n, err)
			}
			awaitSCTPApplication(t, done)
			if _, err := f.lookup(t, mqttSNRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("invalid request reached intake: %v", err)
			}
		})
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
