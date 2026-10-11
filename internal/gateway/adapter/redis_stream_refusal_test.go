//go:build darwin || linux

package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestRedisStreamAdmissionRefusalsLeaveJournalUntouched covers stream entry
// and route refusals; the refusals every carrier shares are in
// TestCarrierAdmission.
func TestRedisStreamAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"target", "operation", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher", "entry", "deleted", "stream"} {
		t.Run(kind, func(t *testing.T) {
			f := newRedisFixture(t)
			request := redisRequest()
			broker := redisStreamBroker()
			entry := redisStreamEntry(t, request)
			route := &broker.Route
			channel := route.Stream
			slots := make(chan struct{}, 1)
			d := f.d
			switch kind {
			case "entry":
				entry.ID = "0-0"
			case "deleted":
				entry.Deleted = true
			case "stream":
				route.Stream = "other"
			case "target":
				route.Target.Name = "#other"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "empty":
				entry.Payload = nil
			case "oversize":
				entry.Payload = make([]byte, rediswire.MaxBulk+1)
			case "channel":
				channel = "other"
			case "response-channel":
				route.ResultStream = route.Stream
			case "no-slots":
				slots = nil
			case "no-dispatcher":
				d = nil
			}
			a := admitRedisStream(t.Context(), d, f.author, broker, channel, entry, slots)
			if a.result != nil || !a.refused && a.failure == nil {
				t.Fatalf("accepted refusal %s: %+v", kind, a)
			}
			if kind == "target" || kind == "operation" {
				// A request outside the route gets a correlated refusal.
				if a.refused || a.failure == nil || a.failure.Code != core.Forbidden || a.failure.Transient || a.requestID != request.RequestID {
					t.Fatalf("correlated forbidden failure: %+v", a)
				}
			} else if !a.refused || a.failure != nil || a.requestID != "" {
				t.Fatalf("non-request correlated: %+v", a)
			}
			if len(slots) != 0 {
				t.Fatal("request slot leaked")
			}
			if _, err := f.lookup(t, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal admitted job", err)
			}
		})
	}
}
