//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSTOMPAdmissionDurableReplayAcrossJournalReopen(t *testing.T) {
	f := newSTOMPAuthorFixture(t)
	request := amqpRequest()
	request.RequestID = "stomp-stable"
	frame := stompRequestFrame(t, request)
	slots := make(chan struct{}, 1)
	var upstream string
	for i := 0; i < 3; i++ {
		frame.Headers[3].Value = strings.Repeat("x", i+1)
		admission := admitSTOMP(t.Context(), f.d, f.in, stompFixtureRoute(), frame, slots)
		if admission.refusal != "" || admission.failure != nil || admission.requestID != request.RequestID || admission.result == nil || admission.result.Result == nil || !admission.result.Result.Durable {
			t.Fatal(admission)
		}
		job, err := f.lookup(t, request.RequestID)
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
			f.d = f.listener(t, stompFixtureListener())
		}
	}
	request.Message = "changed intent"
	a := admitSTOMP(t.Context(), f.d, f.in, stompFixtureRoute(), stompRequestFrame(t, request), slots)
	if a.refusal != "" || a.result != nil || a.failure == nil || a.failure.Code != core.IDConflict || a.failure.Transient {
		t.Fatal(a)
	}
	job, err := f.lookup(t, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message == request.Message {
		t.Fatal(job, err)
	}
}

func TestSTOMPAdmissionPostsPlainRequestAsItsAuthor(t *testing.T) {
	f := newSTOMPAuthorFixture(t)
	request := amqpRequest()
	frame := stompBodyFrame([]byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"exact body\n世界","author":{"nick":"stomp-guest"}}`))
	a := admitSTOMP(t.Context(), f.d, f.in, stompFixtureRoute(), frame, make(chan struct{}, 1))
	if a.refusal != "" || a.failure != nil || a.result == nil || a.result.Result == nil || !a.result.Result.Durable || a.requestID != request.RequestID {
		t.Fatal(a)
	}
	job, err := f.lookup(t, request.RequestID)
	if err != nil || job.Author != (core.Author{Nick: stompTestAuthor}) || job.Request.Target == nil || *job.Request.Target != *request.Target || job.Request.Message != request.Message || job.Request.Operation != core.Send {
		t.Fatal(job, err)
	}
}

func TestSTOMPAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"operation", "body-target", "body-operation", "anonymous", "malformed", "unknown-field", "compact-token", "capacity", "cancelled", "command", "empty-body", "oversize", "destination", "reply", "content-type", "transaction", "receipt-bound"} {
		t.Run(kind, func(t *testing.T) {
			f := newSTOMPAuthorFixture(t)
			request := amqpRequest()
			frame := stompRequestFrame(t, request)
			route := stompFixtureRoute()
			slots := make(chan struct{}, 1)
			ctx := t.Context()
			switch kind {
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "body-target":
				changed := request
				changed.Target = &core.Target{Kind: "room", Name: "#other"}
				frame = stompRequestFrame(t, changed)
			case "anonymous":
				frame = stompBodyFrame([]byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x"}`))
			case "body-operation":
				frame = stompRequestFrame(t, core.Request{Version: 1, RequestID: request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID})
			case "malformed":
				frame = stompBodyFrame([]byte(`{"version":1,"request_id":`))
			case "unknown-field":
				frame = stompBodyFrame([]byte(`{"version":1,"request_id":"amqp-stable-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","principal":"other"}`))
			case "compact-token":
				// A compact token (not JSON) is not a contract request.
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
			case "reply":
				frame.Headers = append(frame.Headers, stompwire.Header{Key: "reply-to", Value: "other"})
			case "content-type":
				frame.Headers[1].Value = "application/jose"
			case "transaction":
				frame.Headers = append(frame.Headers, stompwire.Header{Key: "transaction", Value: "tx"})
			case "receipt-bound":
				frame.Headers[3].Value = strings.Repeat("x", 256)
			}
			a := admitSTOMP(ctx, f.d, f.in, route, frame, slots)
			if a.result != nil || (a.refusal == "" && a.failure == nil) {
				t.Fatal(kind, a)
			}
			switch kind {
			case "malformed", "unknown-field", "compact-token":
				if a.refusal != "invalid STOMP request body" {
					t.Fatal(kind, a)
				}
			case "operation", "body-target", "body-operation":
				if a.refusal != "" || a.failure == nil || a.failure.Code != core.Forbidden || a.requestID != request.RequestID {
					t.Fatal(kind, a)
				}
			case "anonymous":
				if a.failure == nil || a.failure.Code != core.InvalidRequest {
					t.Fatal(kind, a)
				}
			}
			if kind == "capacity" && (a.failure.Code != core.CapacityExceeded || !a.failure.Transient || a.requestID != request.RequestID || len(slots) != 1) {
				t.Fatal(a)
			}
			if _, err := f.lookup(t, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal created job", err)
			}
		})
	}
}
