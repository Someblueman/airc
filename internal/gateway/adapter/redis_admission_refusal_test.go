//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// Broker input a Redis route cannot carry is dropped without a correlated
// result and leaves the journal and request slots untouched; a cancelled
// admission is a correlated transient failure. Pub/Sub and Streams share
// this admission; the route and author refusals every route-owned carrier
// shares run in TestRouteAdmission.
func TestRedisAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	kinds := map[string][]string{
		"pubsub":  {"signed", "cancelled", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher"},
		"streams": {"signed", "cancelled", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher", "entry", "deleted"},
	}
	for carrier, kinds := range kinds {
		for _, kind := range kinds {
			t.Run(carrier+"/"+kind, func(t *testing.T) {
				f := newRedisFixture(t)
				request := redisRequest()
				payload := redisPlainRequest(t, request)
				route, broker := redisAdmissionRoute(), redisStreamBroker()
				channel, stream, entry := route.RequestChannel, broker.Route.Stream, rediswire.StreamEntry{ID: "1-0"}
				slots := make(chan struct{}, 1)
				ctx, d := t.Context(), f.d
				switch kind {
				case "signed":
					// A compact JWS is not a contract request and is not verified here.
					payload = []byte("eyJhbGciOiJFZERTQSJ9.eyJ2ZXJzaW9uIjoxfQ.c2lnbmF0dXJl")
				case "cancelled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "empty":
					payload = nil
				case "oversize":
					payload = make([]byte, rediswire.MaxBulk+1)
				case "channel":
					channel, stream = "other", "other"
				case "response-channel":
					route.ResponseChannel, broker.Route.ResultStream = route.RequestChannel, broker.Route.Stream
				case "no-slots":
					slots = nil
				case "no-dispatcher":
					d = nil
				case "entry":
					entry.ID = "0-0"
				case "deleted":
					entry.Deleted = true
				}
				var a redisAdmission
				if carrier == "pubsub" {
					a = admitRedis(ctx, d, f.author, "route:redis", route, channel, payload, slots)
				} else {
					entry.Payload = payload
					a = admitRedisStream(ctx, d, f.author, broker, stream, entry, slots)
				}
				if kind == "cancelled" {
					if a.refused || a.result != nil || a.failure == nil || !a.failure.Transient || a.requestID != request.RequestID {
						t.Fatalf("correlated transient failure: %+v", a)
					}
				} else if !a.refused || a.result != nil || a.failure != nil || a.requestID != "" {
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
