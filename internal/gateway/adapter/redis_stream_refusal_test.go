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
	for _, kind := range []string{"principal", "target", "body-target", "operation", "malformed", "unknown-field", "signed", "capacity", "cancelled", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher", "entry", "deleted", "stream"} {
		t.Run(kind, func(t *testing.T) {
			f := newRedisFixture(t)
			request := redisRequest()
			broker := redisStreamBroker()
			entry := redisStreamEntry(t, request)
			route := &broker.Route
			channel := route.Stream
			slots := make(chan struct{}, 1)
			ctx, d := t.Context(), f.d
			switch kind {
			case "entry":
				entry.ID = "0-0"
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
			case "body-target":
				// The principal may post to #other, but this route is fixed to #fixture.
				request.Target = &core.Target{Kind: "room", Name: "#other"}
				entry.Payload = redisPlainRequest(t, request)
			case "malformed":
				entry.Payload = []byte(`{"version":1,"request_id":"redis-stable-request",`)
			case "unknown-field":
				entry.Payload = []byte(`{"version":1,"request_id":"redis-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","author":"other"}`)
			case "signed":
				// A compact JWS is not a contract request and is not verified here.
				entry.Payload = []byte("eyJhbGciOiJFZERTQSJ9.eyJ2ZXJzaW9uIjoxfQ.c2lnbmF0dXJl")
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
				if a.refused || a.failure == nil || !a.failure.Transient || a.requestID != request.RequestID {
					t.Fatalf("authenticated failure: %+v", a)
				}
			} else if !a.refused || a.failure != nil || a.requestID != "" {
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
