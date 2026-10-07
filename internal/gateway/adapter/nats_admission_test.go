//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestNATSAdmissionDurableReplayAcrossReopenAndSubjects(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	request := amqpRequest()
	request.RequestID = "nats-stable"
	frame := natsAdmissionFrame(t, f, request, 0)
	route := natsAdmissionRoute(f)
	slots := make(chan struct{}, 1)
	var upstream string
	for i := range 3 {
		if i == 2 {
			route.RequestSubject, route.ResponseSubject = "other.request", "other.response"
			frame.Subject, frame.Reply = route.RequestSubject, route.ResponseSubject
		}
		a := admitNATS(t.Context(), f.d, f.identity, route, frame, slots)
		if a.refused || a.failure != nil || a.requestID != request.RequestID || a.result == nil || a.result.Result == nil || !a.result.Result.Durable {
			t.Fatalf("durable admission: %+v", a)
		}
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
		if err != nil || job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target || !job.Result.Durable || job.Result.SendAttempts != 0 || (upstream != "" && upstream != job.UpstreamID) {
			t.Fatal("journal intent changed", err)
		}
		upstream = job.UpstreamID
		if len(slots) != 0 {
			t.Fatal("request slot leaked")
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
			f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, route.Operations)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	request.Message = "changed intent"
	changed := natsAdmissionFrame(t, f, request, 0)
	changed.Subject, changed.Reply = route.RequestSubject, route.ResponseSubject
	a := admitNATS(t.Context(), f.d, f.identity, route, changed, slots)
	if a.refused || a.result != nil || a.failure == nil || a.failure.Code != core.IDConflict || a.failure.Transient {
		t.Fatalf("conflict: %+v", a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message == request.Message {
		t.Fatal("conflict changed job", err)
	}
}
