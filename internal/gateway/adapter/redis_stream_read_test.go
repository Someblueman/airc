//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamAdmissionParentDeadlineClosesNativeRead(t *testing.T) {
	for _, op := range []core.Operation{core.ContextRead, core.HistoryRead, core.Search} {
		t.Run(string(op), func(t *testing.T) {
			f := newRedisFixture(t)
			assertNativeClosed := stalledNativeRead(t, f.c.Bindings[0].Address)
			broker := redisStreamBroker()
			request := core.Request{Version: 1, RequestID: "redis-read", Operation: op, Target: redisRequest().Target, Limit: 1}
			if op == core.ContextRead {
				request.ParentID = strings.Repeat("a", 32)
			}
			if op == core.Search {
				request.Query = "exact"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			started := time.Now()
			slots := make(chan struct{}, 1)
			a := admitRedisStream(ctx, f.d, broker, broker.Route.Stream, redisStreamEntry(t, request), slots)
			if a.refused || a.result != nil || a.failure == nil || !a.failure.Transient || a.requestID != request.RequestID || time.Since(started) > time.Second || len(slots) != 0 {
				t.Fatalf("bounded read attempt: %+v", a)
			}
			assertNativeClosed()
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("read admitted mutation", err)
			}
		})
	}
}
