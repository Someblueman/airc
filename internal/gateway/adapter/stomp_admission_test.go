//go:build darwin || linux

package adapter

import (
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestSTOMPAdmissionRefusalsLeaveJournalUntouched covers SEND frame metadata
// and route refusals; the refusals every carrier shares are in
// TestCarrierAdmission.
func TestSTOMPAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"operation", "command", "empty-body", "oversize", "destination", "reply", "content-type", "transaction", "receipt-bound"} {
		t.Run(kind, func(t *testing.T) {
			f := newSTOMPAuthorFixture(t)
			request := amqpRequest()
			frame := stompRequestFrame(t, request)
			route := stompFixtureRoute()
			slots := make(chan struct{}, 1)
			switch kind {
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
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
			a := admitSTOMP(t.Context(), f.d, f.in, route, frame, slots)
			if a.result != nil || (a.refusal == "" && a.failure == nil) || len(slots) != 0 {
				t.Fatal(kind, a)
			}
			if kind == "operation" && (a.refusal != "" || a.failure == nil || a.failure.Code != core.Forbidden || a.requestID != request.RequestID) {
				t.Fatal(kind, a)
			}
			if _, err := f.lookup(t, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal created job", err)
			}
		})
	}
}
