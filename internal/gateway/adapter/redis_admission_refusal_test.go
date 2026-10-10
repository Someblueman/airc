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

func TestRedisAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"principal", "target", "operation", "signature", "unsigned", "expired", "future", "capacity", "cancelled", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			request := amqpRequest()
			request.RequestID = "redis-refusal"
			payload := redisSignedRequest(t, f, request, 0)
			route := redisAdmissionRoute(f)
			channel := route.RequestChannel
			slots := make(chan struct{}, 1)
			ctx, d := t.Context(), f.d
			switch kind {
			case "principal":
				route.Principal = "other"
			case "target":
				route.Target.Name = "#other"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "signature":
				payload[len(payload)-1] ^= 1
			case "unsigned":
				payload = []byte(`{"request_id":"forged","message":"unsigned"}`)
			case "expired":
				payload = redisSignedRequest(t, f, request, -400)
			case "future":
				payload = redisSignedRequest(t, f, request, 400)
			case "capacity":
				slots <- struct{}{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
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
			a := admitRedis(ctx, d, route, channel, payload, slots)
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
