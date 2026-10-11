//go:build darwin || linux

package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestRedisAdmissionRefusalsLeaveJournalUntouched covers the route and
// addressing refusals of Redis Pub/Sub and Streams, which share one request
// admission; the refusals every carrier shares are in TestCarrierAdmission.
func TestRedisAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	shared := []string{"target", "operation", "empty", "oversize", "no-slots", "no-dispatcher", "channel", "response-channel"}
	for carrier, kinds := range map[string][]string{
		"pubsub":  shared,
		"streams": append(shared, "entry", "deleted", "stream"),
	} {
		for _, kind := range kinds {
			t.Run(carrier+"/"+kind, func(t *testing.T) {
				t.Parallel()
				f := newRedisFixture(t)
				request := redisRequest()
				payload := redisPlainRequest(t, request)
				route := redisAdmissionRoute()
				broker := redisStreamBroker()
				entry := rediswire.StreamEntry{ID: "1-0"}
				channel := route.RequestChannel
				if carrier == "streams" {
					channel = broker.Route.Stream
				}
				slots := make(chan struct{}, 1)
				d := f.d
				switch kind {
				case "target":
					route.Target.Name, broker.Route.Target.Name = "#other", "#other"
				case "operation":
					route.Operations, broker.Route.Operations = []core.Operation{core.HistoryRead}, []core.Operation{core.HistoryRead}
				case "empty":
					payload = nil
				case "oversize":
					payload = make([]byte, rediswire.MaxBulk+1)
				case "no-slots":
					slots = nil
				case "no-dispatcher":
					d = nil
				case "channel":
					channel = "other"
				case "response-channel":
					route.ResponseChannel, broker.Route.ResultStream = route.RequestChannel, broker.Route.Stream
				case "entry":
					entry.ID = "0-0"
				case "deleted":
					entry.Deleted = true
				case "stream":
					broker.Route.Stream = "other"
				}
				var a redisAdmission
				if carrier == "pubsub" {
					a = admitRedis(t.Context(), d, f.author, "route:redis", route, channel, payload, slots)
				} else {
					entry.Payload = payload
					a = admitRedisStream(t.Context(), d, f.author, broker, channel, entry, slots)
				}
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
}
