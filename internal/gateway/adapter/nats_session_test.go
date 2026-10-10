//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSSessionSignedReplayConflictAndSubscriptionLimit(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	sealer, pub, _, _ := natsResultFixture(t)
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	_, dial, token, cancel, done := natsDispatchListenerFixture(t, slots, 1, f, sealer, requests)
	client := dial()
	r, connected := natsClientConnect(t, client, token)
	if connected.Command != "+OK" {
		t.Fatal("CONNECT refused")
	}
	if _, err := client.Write([]byte("SUB airc.response 7\r\nUNSUB 7 3\r\n")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if reply, err := natswire.Read(r); err != nil || reply.Command != "+OK" {
			t.Fatal("subscription refused", err)
		}
	}
	request := amqpRequest()
	request.RequestID = "nats-session"
	var upstream string
	for i := range 3 {
		if i == 2 {
			request.Message = "conflicting intent"
		}
		wire, err := natswire.Encode(natsAdmissionFrame(t, f, request, 0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.Write(wire); err != nil {
			t.Fatal(err)
		}
		ack, err := natswire.Read(r)
		if err != nil || ack.Command != "+OK" {
			t.Fatal("publication protocol acknowledgement", err)
		}
		message, err := natswire.Read(r)
		if err != nil || message.Command != "MSG" || message.Subject != "airc.response" || message.SID != "7" {
			t.Fatal("response routing", err)
		}
		scope := natsResponseScope("fixture", f.identity.ID(), f.identity.Binding(), natsAdmissionRoute(f))
		plain, err := auth.OpenSignedResponse(string(message.Payload), "response", pub, scope, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var result NATSResult
		if err := json.Unmarshal(plain, &result); err != nil {
			t.Fatal(err)
		}
		if result.RequestID != request.RequestID {
			t.Fatal("request correlation")
		}
		if i < 2 {
			if result.Result == nil || !result.Result.Result.Durable || result.Result.Result.Accepted != nil {
				t.Fatal("durability/acceptance")
			}
		} else if result.Result != nil || result.Error == nil || result.Error.Code != core.IDConflict || result.Error.Transient {
			t.Fatal("conflict changed")
		}
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
		if err != nil || job.Result.SendAttempts != 0 || (upstream != "" && upstream != job.UpstreamID) || job.Request.Message == "conflicting intent" {
			t.Fatal("replay changed journal", err)
		}
		upstream = job.UpstreamID
		if len(requests) != 0 {
			t.Fatal("request capacity leak")
		}
	}
	// The third complete MSG consumed the automatic subscription limit.
	wire, err := natswire.Encode(natsAdmissionFrame(t, f, amqpRequest(), 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	if reply, err := natswire.Read(r); err != nil || reply.Command != "-ERR" {
		t.Fatal("unsubscribed publication admitted", err)
	}
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("shutdown capacity leak")
	}
}
