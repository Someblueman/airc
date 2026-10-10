//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSTOMPAdmissionDurableReplayAcrossJournalReopen(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	request := amqpRequest()
	request.RequestID = "stomp-stable"
	frame := stompRequestFrame(t, request)
	slots := make(chan struct{}, 1)
	var upstream string
	for i := 0; i < 3; i++ {
		frame.Headers[3].Value = strings.Repeat("x", i+1)
		admission := admitSTOMP(t.Context(), f.d, f.identity, stompAdmissionRoute(f), frame, slots)
		if admission.refusal != "" || admission.failure != nil || admission.requestID != request.RequestID || admission.result == nil || admission.result.Result == nil || !admission.result.Result.Durable {
			t.Fatal(admission)
		}
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
		if err != nil || job.Request.Message != request.Message || !job.Result.Durable || job.Result.SendAttempts != 0 || (upstream != "" && upstream != job.UpstreamID) {
			t.Fatal(job, err)
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
			f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, stompAdmissionRoute(f).Operations)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	request.Message = "changed intent"
	a := admitSTOMP(t.Context(), f.d, f.identity, stompAdmissionRoute(f), stompRequestFrame(t, request), slots)
	if a.refusal != "" || a.result != nil || a.failure == nil || a.failure.Code != core.IDConflict || a.failure.Transient {
		t.Fatal(a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message == request.Message {
		t.Fatal(job, err)
	}
}

func TestSTOMPAdmissionPostsPlainRequestAsConnectionPrincipal(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	request := amqpRequest()
	frame := stompBodyFrame([]byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"exact body\n世界"}`))
	a := admitSTOMP(t.Context(), f.d, f.identity, stompAdmissionRoute(f), frame, make(chan struct{}, 1))
	if a.refusal != "" || a.failure != nil || a.result == nil || a.result.Result == nil || !a.result.Result.Durable || a.requestID != request.RequestID {
		t.Fatal(a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.Request.Target == nil || *job.Request.Target != *request.Target || job.Request.Message != request.Message || job.Request.Operation != core.Send {
		t.Fatal(job, err)
	}
}

func TestSTOMPAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"principal", "target", "operation", "body-target", "body-unauthorized-target", "body-operation", "malformed", "unknown-field", "envelope", "capacity", "cancelled", "command", "empty-body", "oversize", "destination", "publisher", "reply", "content-type", "transaction", "unknown", "receipt-bound"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			request := amqpRequest()
			frame := stompRequestFrame(t, request)
			route := stompAdmissionRoute(f)
			slots := make(chan struct{}, 1)
			ctx := t.Context()
			switch kind {
			case "principal":
				route.Principal = "other"
			case "target":
				route.Target.Name = "#other"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "body-target":
				changed := request
				changed.Target = &core.Target{Kind: "room", Name: "#other"}
				frame = stompRequestFrame(t, changed)
			case "body-unauthorized-target":
				changed := request
				changed.Target = &core.Target{Kind: "room", Name: "#elsewhere"}
				frame = stompRequestFrame(t, changed)
			case "body-operation":
				// The principal may get receipts, but this carrier and route may not.
				frame = stompRequestFrame(t, core.Request{Version: 1, RequestID: request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID})
			case "malformed":
				frame = stompBodyFrame([]byte(`{"version":1,"request_id":`))
			case "unknown-field":
				frame = stompBodyFrame([]byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","principal":"other"}`))
			case "envelope":
				// A former signed-envelope token is not a contract request.
				frame = stompBodyFrame([]byte("eyJhbGciOiJFZERTQSJ9.e30.c2ln"))
			case "capacity":
				slots <- struct{}{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "command":
				frame.Command = "MESSAGE"
			case "empty-body":
				frame.Body = nil
			case "oversize":
				frame.Body = make([]byte, stompwire.MaxBody+1)
			case "destination":
				frame.Headers[0].Value = "other"
			case "publisher":
				frame.Headers = append(frame.Headers, stompwire.Header{Key: "user-id", Value: "other"})
			case "reply":
				frame.Headers = append(frame.Headers, stompwire.Header{Key: "reply-to", Value: "other"})
			case "content-type":
				frame.Headers[1].Value = "application/jose"
			case "transaction":
				frame.Headers = append(frame.Headers, stompwire.Header{Key: "transaction", Value: "tx"})
			case "unknown":
				frame.Headers = append(frame.Headers, stompwire.Header{Key: "expires", Value: "9999999999999"})
			case "receipt-bound":
				frame.Headers[3].Value = strings.Repeat("x", 256)
			}
			a := admitSTOMP(ctx, f.d, f.identity, route, frame, slots)
			if a.result != nil || (a.refusal == "" && a.failure == nil) {
				t.Fatal(kind, a)
			}
			switch kind {
			case "malformed", "unknown-field", "envelope":
				if a.refusal != "invalid STOMP request body" {
					t.Fatal(kind, a)
				}
			case "principal", "target", "operation", "body-target", "body-unauthorized-target", "body-operation":
				if a.refusal != "unauthorized STOMP request" {
					t.Fatal(kind, a)
				}
			}
			if kind == "capacity" && (a.failure.Code != core.CapacityExceeded || !a.failure.Transient || a.requestID != request.RequestID || len(slots) != 1) {
				t.Fatal(a)
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal created job", err)
			}
		})
	}
}
