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
	frame := stompAdmissionFrame(t, f, request, 0)
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
	a := admitSTOMP(t.Context(), f.d, f.identity, stompAdmissionRoute(f), stompAdmissionFrame(t, f, request, 0), slots)
	if a.refusal != "" || a.result != nil || a.failure == nil || a.failure.Code != core.IDConflict || a.failure.Transient {
		t.Fatal(a)
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message == request.Message {
		t.Fatal(job, err)
	}
}

func TestSTOMPAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"principal", "target", "operation", "signature", "expired", "capacity", "cancelled", "command", "empty-body", "oversize", "destination", "publisher", "reply", "content-type", "transaction", "unknown", "receipt-bound"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			request := amqpRequest()
			frame := stompAdmissionFrame(t, f, request, 0)
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
			case "signature":
				frame.Body[len(frame.Body)-1] ^= 1
			case "expired":
				frame = stompAdmissionFrame(t, f, request, -400)
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
				frame.Headers[1].Value = "application/json"
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
			if kind == "capacity" && (a.failure.Code != core.CapacityExceeded || !a.failure.Transient || a.requestID != request.RequestID || len(slots) != 1) {
				t.Fatal(a)
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal created job", err)
			}
		})
	}
}
