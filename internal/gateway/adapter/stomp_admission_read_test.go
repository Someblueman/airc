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

func TestSTOMPAdmissionParentDeadlineClosesNativeAttempt(t *testing.T) {
	for _, op := range []core.Operation{core.ContextRead, core.HistoryRead, core.Search} {
		t.Run(string(op), func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			assertNativeClosed := stalledNativeRead(t, f.c.Bindings[0].Address)
			request := core.Request{Version: 1, RequestID: "stomp-read", Operation: op, Target: amqpRequest().Target, Limit: 1}
			if op == core.ContextRead {
				request.ParentID = strings.Repeat("a", 32)
			}
			if op == core.Search {
				request.Query = "exact"
			}
			frame := stompRequestFrame(t, request)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			started := time.Now()
			slots := make(chan struct{}, 1)
			admission := admitSTOMP(ctx, f.d, f.identity, stompAdmissionRoute(f), frame, slots)
			if admission.refusal != "" || admission.result != nil || admission.failure == nil || !admission.failure.Transient || admission.requestID != request.RequestID || time.Since(started) > time.Second || len(slots) != 0 {
				t.Fatal(admission, time.Since(started))
			}
			assertNativeClosed()
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("read admitted mutation", err)
			}
		})
	}
}
