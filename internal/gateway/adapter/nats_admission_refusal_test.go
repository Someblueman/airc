//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/natswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestNATSAdmissionRefusalsLeaveJournalUntouched(t *testing.T) {
	for _, kind := range []string{"principal", "identity", "target", "operation", "signature", "expired", "capacity", "cancelled", "command", "empty", "oversize", "subject", "reply", "missing-reply", "extra-fields", "no-slots", "no-dispatcher"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			request := amqpRequest()
			request.RequestID = "nats-refusal"
			frame := natsAdmissionFrame(t, f, request, 0)
			route := natsAdmissionRoute(f)
			identity := f.identity
			slots := make(chan struct{}, 1)
			ctx := t.Context()
			d := f.d
			switch kind {
			case "principal":
				route.Principal = "other"
			case "identity":
				identity = auth.Identity{}
			case "target":
				route.Target.Name = "#other"
			case "operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "signature":
				frame.Payload[len(frame.Payload)-1] ^= 1
			case "expired":
				frame = natsAdmissionFrame(t, f, request, -400)
			case "capacity":
				slots <- struct{}{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "command":
				frame.Command = "MSG"
			case "empty":
				frame.Payload = nil
			case "oversize":
				frame.Payload = make([]byte, natswire.MaxPayload+1)
			case "subject":
				frame.Subject = "other"
			case "reply":
				frame.Reply = "other"
			case "missing-reply":
				frame.Reply = ""
			case "extra-fields":
				frame.SID = "forged"
			case "no-slots":
				slots = nil
			case "no-dispatcher":
				d = nil
			}
			a := admitNATS(ctx, d, identity, route, frame, slots)
			if a.result != nil || (!a.refused && a.failure == nil) {
				t.Fatalf("accepted refusal %s: %+v", kind, a)
			}
			if kind == "capacity" && (a.failure == nil || a.failure.Code != core.CapacityExceeded || !a.failure.Transient || a.requestID != request.RequestID || len(slots) != 1) {
				t.Fatalf("capacity correlation: %+v", a)
			}
			if kind != "capacity" && len(slots) != 0 {
				t.Fatal("request slot leaked")
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal admitted job", err)
			}
		})
	}
}
