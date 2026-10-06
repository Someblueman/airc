//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestAMQPAdmissionDurableReplaySurvivesJournalReopen(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	slots := make(chan struct{}, 1)
	request := amqpRequest()
	in := f.incoming(t, request, 0)
	var upstream string
	for i := 0; i < 3; i++ {
		in.id = uint32(99 - i)
		in.tag = []byte{byte(i)} // Wire correlation never drives replay.
		admission := admitAMQP(t.Context(), f.d, f.identity, f.route(), in, slots)
		if admission.condition != "" || admission.failure != nil || admission.requestID != request.RequestID || admission.result == nil || !admission.result.Result.Durable {
			t.Fatal(admission)
		}
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
		if err != nil || job.Request.Message != request.Message || !job.Result.Durable || job.Result.SendAttempts != 0 || upstream != "" && upstream != job.UpstreamID {
			t.Fatal(job, err)
		}
		upstream = job.UpstreamID
		if len(slots) != 0 {
			t.Fatal("admission leaked capacity")
		}
		if i == 0 {
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			f.s, err = store.Open(t.Context(), f.c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.s.Close() })
			f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, f.route().Operations)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	conflicting := request
	conflicting.Message = "different command"
	admission := admitAMQP(t.Context(), f.d, f.identity, f.route(), f.incoming(t, conflicting, 0), slots)
	if admission.condition != "" || admission.result != nil || admission.failure == nil || admission.failure.Code != core.IDConflict || admission.failure.Transient {
		t.Fatal(admission)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message != request.Message {
		t.Fatal(job, err)
	}
}
func TestAMQPAdmissionRefusesBeforeIntake(t *testing.T) {
	for _, kind := range []string{"principal", "target", "operation", "signature", "expired", "capacity", "cancelled", "partial"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			slots := make(chan struct{}, 1)
			request := amqpRequest()
			in := f.incoming(t, request, 0)
			route := f.route()
			ctx := t.Context()
			switch kind {
			case "principal":
				route.Principal = "other"
			case "target":
				route.Target.Name = "#other" // Both targets are principal-authorized.
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "signature":
				in.encoded[len(in.encoded)-1] ^= 1
			case "expired":
				in = f.incoming(t, request, -400)
			case "capacity":
				slots <- struct{}{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "partial":
				in.complete = false
			}
			admission := admitAMQP(ctx, f.d, f.identity, route, in, slots)
			if admission.result != nil || admission.condition == "" && admission.failure == nil {
				t.Fatal(kind, admission)
			}
			if kind == "capacity" && (admission.failure.Code != core.CapacityExceeded || !admission.failure.Transient || len(slots) != 1 || admission.requestID != request.RequestID) {
				t.Fatal(admission)
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal reached intake", err)
			}
		})
	}
}
