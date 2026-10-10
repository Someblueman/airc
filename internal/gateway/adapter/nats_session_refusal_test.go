//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/natswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestNATSSessionRefusesBeforeIntakeAndPreservesForeignCapacity(t *testing.T) {
	for _, kind := range []string{"capacity", "subject", "reply", "signature", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			sealer, pub, _, _ := natsResultFixture(t)
			requests := make(chan struct{}, 1)
			if kind == "capacity" {
				requests <- struct{}{}
			}
			_, dial, token, cancel, done := natsDispatchListenerFixture(t, make(chan struct{}, 1), 1, f, sealer, requests)
			client := dial()
			r, connected := natsClientConnect(t, client, token)
			if connected.Command != "+OK" {
				t.Fatal("CONNECT refused")
			}
			if kind != "subscription" {
				if _, err := client.Write([]byte("SUB airc.response 1\r\n")); err != nil {
					t.Fatal(err)
				}
				if reply, err := natswire.Read(r); err != nil || reply.Command != "+OK" {
					t.Fatal("SUB", err)
				}
			}
			request := amqpRequest()
			frame := natsAdmissionFrame(t, f, request, 0)
			switch kind {
			case "subject":
				frame.Subject = "other.request"
			case "reply":
				frame.Reply = "other.response"
			case "signature":
				frame.Payload = []byte("invalid")
			}
			wire, err := natswire.Encode(frame)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Write(wire); err != nil {
				t.Fatal(err)
			}
			reply, err := natswire.Read(r)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "capacity" {
				if reply.Command != "+OK" {
					t.Fatal("protocol acknowledgement")
				}
				reply, err = natswire.Read(r)
				if err != nil || reply.Command != "MSG" {
					t.Fatal("capacity result", err)
				}
				scope := natsResponseScope("fixture", f.identity.ID(), f.identity.Binding(), natsAdmissionRoute(f))
				plain, err := auth.OpenSignedResponse(string(reply.Payload), "response", pub, scope, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				var result NATSResult
				if err := json.Unmarshal(plain, &result); err != nil {
					t.Fatal(err)
				}
				if result.RequestID != request.RequestID || result.Result != nil || result.Error == nil || result.Error.Code != core.CapacityExceeded || !result.Error.Transient {
					t.Fatal("capacity correlation")
				}
			} else if reply.Command != "-ERR" {
				t.Fatal("unauthorized publication acknowledged")
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refusal reached journal", err)
			}
			cancel()
			if err := amqpListenerFinished(t, done); err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "capacity" {
				want = 1
			}
			if len(requests) != want {
				t.Fatal("foreign capacity consumed or leaked")
			}
		})
	}
}
