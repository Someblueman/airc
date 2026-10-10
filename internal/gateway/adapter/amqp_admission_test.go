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
	in := f.incoming(t, request)
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
	admission := admitAMQP(t.Context(), f.d, f.identity, f.route(), f.incoming(t, conflicting), slots)
	if admission.condition != "" || admission.result != nil || admission.failure == nil || admission.failure.Code != core.IDConflict || admission.failure.Transient {
		t.Fatal(admission)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message != request.Message {
		t.Fatal(job, err)
	}
}
func TestAMQPAdmissionPostsPlainRequestAsConnectionPrincipal(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	request := amqpRequest()
	admission := admitAMQP(t.Context(), f.d, f.identity, f.route(), amqpIncomingBody(t, []byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"exact body\n世界"}`)), make(chan struct{}, 1))
	if admission.condition != "" || admission.failure != nil || admission.result == nil || admission.result.Result == nil || !admission.result.Result.Durable {
		t.Fatal(admission)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.Request.Target == nil || *job.Request.Target != *request.Target || job.Request.Message != request.Message || job.Request.Operation != core.Send {
		t.Fatal(job, err)
	}
}

func TestAMQPAdmissionRefusesBeforeIntake(t *testing.T) {
	for _, kind := range []string{"principal", "target", "operation", "body-target", "body-unauthorized-target", "body-operation", "malformed", "unknown-field", "envelope", "capacity", "cancelled", "partial"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			slots := make(chan struct{}, 1)
			request := amqpRequest()
			in := f.incoming(t, request)
			route := f.route()
			ctx := t.Context()
			want := ""
			switch kind {
			case "principal":
				route.Principal = "other"
				want = "amqp:unauthorized-access"
			case "target":
				route.Target.Name = "#other" // Both targets are principal-authorized.
				want = "amqp:unauthorized-access"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
				want = "amqp:unauthorized-access"
			case "body-target":
				changed := request
				changed.Target = &core.Target{Kind: "room", Name: "#other"}
				in = f.incoming(t, changed)
				want = "amqp:unauthorized-access"
			case "body-unauthorized-target":
				changed := request
				changed.Target = &core.Target{Kind: "room", Name: "#elsewhere"}
				in = f.incoming(t, changed)
				want = "amqp:unauthorized-access"
			case "body-operation":
				// The principal may get receipts, but this carrier and route may not.
				in = f.incoming(t, core.Request{Version: 1, RequestID: request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID})
				want = "amqp:unauthorized-access"
			case "malformed":
				in = amqpIncomingBody(t, []byte(`{"version":1,"request_id":`))
				want = "amqp:decode-error"
			case "unknown-field":
				in = amqpIncomingBody(t, []byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","principal":"other"}`))
				want = "amqp:decode-error"
			case "envelope":
				// A former signed-envelope token is not a contract request.
				in = amqpIncomingBody(t, []byte("eyJhbGciOiJFZERTQSJ9.e30.c2ln"))
				want = "amqp:decode-error"
			case "capacity":
				slots <- struct{}{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "partial":
				in.complete = false
				want = "amqp:illegal-state"
			}
			admission := admitAMQP(ctx, f.d, f.identity, route, in, slots)
			if admission.result != nil || admission.condition == "" && admission.failure == nil || want != "" && admission.condition != want {
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
