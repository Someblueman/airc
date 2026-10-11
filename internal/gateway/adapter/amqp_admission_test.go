//go:build darwin || linux

package adapter

import (
	"context"
	"maps"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestAMQPAdmissionDurableReplaySurvivesJournalReopen(t *testing.T) {
	f := newAMQPAuthorFixture(t)
	slots := make(chan struct{}, 1)
	request := amqpAuthorRequest()
	in := f.body(t, request)
	for i := 0; i < 3; i++ {
		in.id = uint32(99 - i)
		in.tag = []byte{byte(i)} // Wire correlation never drives replay.
		admission := admitAMQP(t.Context(), f.d, f.in, f.route(), in, slots)
		if admission.condition != "" || admission.failure != nil || admission.requestID != "amqp-stable-request" || admission.result == nil || !admission.result.Result.Durable {
			t.Fatal(admission)
		}
		if f.jobs(t, "amqp-stable-request") != 1 || len(slots) != 0 {
			t.Fatal("replay duplicated intake or leaked capacity")
		}
		if i == 0 {
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			f.s, err = store.Open(t.Context(), f.c)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.s.Close() })
			f.d = f.listener(t, amqpFixtureListener())
		}
	}
	if a := f.owner(t, "amqp-guest", false, "amqp-stable-request"); a != (core.Author{Nick: "amqp-guest"}) {
		t.Fatal("wrong author", a)
	}
	conflicting := maps.Clone(request)
	conflicting["message"] = "different command"
	admission := admitAMQP(t.Context(), f.d, f.in, f.route(), f.body(t, conflicting), slots)
	if admission.condition != "" || admission.result != nil || admission.failure == nil || admission.failure.Code != core.IDConflict || admission.failure.Transient {
		t.Fatal(admission)
	}
}

func TestAMQPAdmissionPostsAsRegisteredAuthorAndAmqpValueBody(t *testing.T) {
	f := newAMQPAuthorFixture(t)
	request := amqpAuthorRequest()
	request["author"] = map[string]string{"nick": "carol", "token": authorTestToken}
	admission := admitAMQP(t.Context(), f.d, f.in, f.route(), f.body(t, request), make(chan struct{}, 1))
	if admission.condition != "" || admission.failure != nil || admission.result == nil || admission.result.Result == nil || !admission.result.Result.Durable {
		t.Fatal(admission)
	}
	if a := f.owner(t, "carol", true, "amqp-stable-request"); a != (core.Author{Nick: "carol", Registered: true}) {
		t.Fatal("wrong author", a)
	}
}

func TestAMQPAdmissionRefusesBeforeIntake(t *testing.T) {
	for _, kind := range []string{"route-target", "route-operation", "body-target", "body-operation", "anonymous", "malformed", "unknown-field", "envelope", "capacity", "cancelled", "partial"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAuthorFixture(t)
			slots := make(chan struct{}, 1)
			request := amqpAuthorRequest()
			in := f.body(t, request)
			route := f.route()
			ctx := t.Context()
			want, code := "", core.ErrorCode("")
			switch kind {
			case "route-target":
				route.Target.Name = "#other"
				code = core.Forbidden
			case "route-operation":
				route.Operations = []core.Operation{core.HistoryRead}
				code = core.Forbidden
			case "body-target":
				request["target"] = map[string]string{"kind": "room", "name": "#other"}
				in = f.body(t, request)
				code = core.Forbidden
			case "body-operation":
				in = f.body(t, map[string]any{"version": 1, "request_id": "amqp-stable-request", "operation": "receipt.get", "lookup_id": "amqp-stable-request", "author": map[string]string{"nick": "amqp-guest"}})
				code = core.Forbidden
			case "anonymous":
				delete(request, "author")
				in = f.body(t, request)
				code = core.InvalidRequest
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
				code = core.CapacityExceeded
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "partial":
				in.complete = false
				want = "amqp:illegal-state"
			}
			admission := admitAMQP(ctx, f.d, f.in, route, in, slots)
			if admission.result != nil || admission.condition == "" && admission.failure == nil || want != "" && admission.condition != want || code != "" && (admission.failure == nil || admission.failure.Code != code) {
				t.Fatal(kind, admission, admission.failure)
			}
			if kind == "capacity" && (!admission.failure.Transient || len(slots) != 1 || admission.requestID != "amqp-stable-request") {
				t.Fatal(admission)
			}
			if f.jobs(t, "amqp-stable-request") != 0 {
				t.Fatal("refusal reached intake")
			}
		})
	}
}
