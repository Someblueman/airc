//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func TestSTOMPDispatchFailureHasSignedERRORWithoutSuccessReceipt(t *testing.T) {
	for _, kind := range []string{"capacity", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
			_, dial, token, cancel, done, fixture, pub := stompDispatchFixtureRequests(t, slots, 1, requests)
			client := dial()
			r, connected := stompClientConnect(t, client, token)
			if connected.Command != "CONNECTED" {
				t.Fatal(connected)
			}
			stompClientWrite(t, client, stompSubscribeFrame())
			stompReadFrame(t, r, "RECEIPT")
			request := amqpRequest()
			want := core.CapacityExceeded
			if kind == "capacity" {
				requests <- struct{}{}
			} else {
				stompClientWrite(t, client, stompAdmissionFrame(t, fixture, request, 0))
				stompReadFrame(t, r, "RECEIPT")
				message := stompReadFrame(t, r, "MESSAGE")
				ack, _ := message.Header("ack")
				stompClientWrite(t, client, stompwire.Frame{Command: "ACK", Headers: []stompwire.Header{{Key: "id", Value: ack}, {Key: "receipt", Value: "acked"}}})
				stompReadFrame(t, r, "RECEIPT")
				request.Message = "conflicting body"
				want = core.IDConflict
			}
			stompClientWrite(t, client, stompAdmissionFrame(t, fixture, request, 0))
			failure := stompReadFrame(t, r, "ERROR")
			if receipt, _ := failure.Header("receipt-id"); receipt != "wire-receipt" {
				t.Fatal(failure)
			}
			plain, err := auth.OpenSignedResponse(string(failure.Body), "response", pub, stompResponseScope(fixture.c.Realm, fixture.identity.ID(), fixture.identity.Binding(), stompAdmissionRoute(fixture)), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var result STOMPResult
			if err := json.Unmarshal(plain, &result); err != nil {
				t.Fatal(err)
			}
			if result.RequestID != request.RequestID || result.Result != nil || result.Error == nil || result.Error.Code != want || result.Error.Transient != (kind == "capacity") {
				t.Fatal(result)
			}
			if f, err := stompwire.Read(r); err == nil {
				t.Fatal("frame after terminal failure", f)
			}
			job, err := fixture.s.Lookup(t.Context(), fixture.identity, request.RequestID)
			if kind == "capacity" {
				if err == nil || len(requests) != 1 {
					t.Fatal("refusal admitted intent or stole request slot", job, err)
				}
				<-requests
			} else if err != nil || job.Request.Message != amqpRequest().Message {
				t.Fatal("conflict changed original job", job, err)
			}
			waitAMQPSlots(t, slots, 0)
			cancel()
			amqpListenerFinished(t, done)
		})
	}
}
