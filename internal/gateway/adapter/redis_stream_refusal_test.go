//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"principal", "target", "operation", "signature", "unsigned", "expired", "future", "capacity", "cancelled", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher", "entry", "namespace", "request-id", "deleted", "stream"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			request := amqpRequest()
			broker := redisStreamBroker(f)
			request = redisStreamRequest(t, broker, request, "1-0")
			entry := redisStreamEntry(t, f, request, 0)
			route := &broker.Route
			channel := route.Stream
			slots := make(chan struct{}, 1)
			ctx, d := t.Context(), f.d
			switch kind {
			case "entry":
				entry.ID = "1-1"
			case "namespace":
				broker.Namespace = "other"
			case "request-id":
				request.RequestID = "arbitrary"
				entry.Payload = redisSignedRequest(t, f, request, 0)
			case "deleted":
				entry.Deleted = true
			case "stream":
				route.Stream = "other"
			case "principal":
				route.Principal = "other"
			case "target":
				route.Target.Name = "#other"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "signature":
				entry.Payload[len(entry.Payload)-1] ^= 1
			case "unsigned":
				entry.Payload = []byte(`{"request_id":"forged","message":"unsigned"}`)
			case "expired":
				entry.Payload = redisSignedRequest(t, f, request, -400)
			case "future":
				entry.Payload = redisSignedRequest(t, f, request, 400)
			case "capacity":
				slots <- struct{}{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
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
			a := admitRedisStream(ctx, d, broker, channel, entry, slots)
			if a.result != nil || !a.refused && a.failure == nil {
				t.Fatalf("accepted refusal %s: %+v", kind, a)
			}
			if kind == "capacity" || kind == "cancelled" {
				if a.refused || a.failure == nil || !a.failure.Transient || a.requestID != request.RequestID || a.binding != f.identity.Binding() {
					t.Fatalf("authenticated failure: %+v", a)
				}
			} else if !a.refused || a.failure != nil || a.requestID != "" || a.binding != "" {
				t.Fatalf("unauthorized correlation disclosed: %+v", a)
			}
			if kind == "capacity" {
				if a.failure.Code != core.CapacityExceeded || len(slots) != 1 {
					t.Fatal("capacity correlation or ownership")
				}
			} else if len(slots) != 0 {
				t.Fatal("request slot leaked")
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal admitted job", err)
			}
		})
	}
}
