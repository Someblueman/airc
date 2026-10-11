//go:build darwin || linux

package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestRedisAdmissionRefusalsLeaveJournalUntouched covers Pub/Sub channel and
// route refusals; the refusals every carrier shares are in
// TestCarrierAdmission.
func TestRedisAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"target", "operation", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher"} {
		t.Run(kind, func(t *testing.T) {
			f := newRedisFixture(t)
			request := redisRequest()
			request.RequestID = "redis-refusal"
			payload := redisPlainRequest(t, request)
			route := redisAdmissionRoute()
			channel := route.RequestChannel
			slots := make(chan struct{}, 1)
			d := f.d
			switch kind {
			case "target":
				route.Target.Name = "#other"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "empty":
				payload = nil
			case "oversize":
				payload = make([]byte, rediswire.MaxBulk+1)
			case "channel":
				channel = "other"
			case "response-channel":
				route.ResponseChannel = route.RequestChannel
			case "no-slots":
				slots = nil
			case "no-dispatcher":
				d = nil
			}
			a := admitRedis(t.Context(), d, f.author, "route:redis", route, channel, payload, slots)
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
