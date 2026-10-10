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
	for _, kind := range []string{"principal", "target", "body-target", "operation", "body-operation", "malformed", "unknown-field", "signed", "capacity", "cancelled", "empty", "oversize", "channel", "response-channel", "no-slots", "no-dispatcher"} {
		t.Run(kind, func(t *testing.T) {
			f := newRedisFixture(t)
			request := redisRequest()
			request.RequestID = "redis-refusal"
			payload := redisPlainRequest(t, request)
			route := redisAdmissionRoute()
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
			case "body-target":
				// The principal may post to #other, but this route is fixed to #fixture.
				request.Target = &core.Target{Kind: "room", Name: "#other"}
				payload = redisPlainRequest(t, request)
			case "body-operation":
				route.Operations = []core.Operation{core.HistoryRead}
				payload = redisPlainRequest(t, request)
			case "malformed":
				payload = []byte(`{"version":1,"request_id":"redis-refusal","operation":"send",`)
			case "unknown-field":
				payload = []byte(`{"version":1,"request_id":"redis-refusal","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","author":"other"}`)
			case "signed":
				// A compact JWS is not a contract request and is not verified here.
				payload = []byte("eyJhbGciOiJFZERTQSJ9.eyJ2ZXJzaW9uIjoxfQ.c2lnbmF0dXJl")
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
