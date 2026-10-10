//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/nngwire"
)

func nngClient(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var greeting [8]byte
	if _, err := io.ReadFull(c, greeting[:]); err != nil || greeting != nngwire.Greeting() {
		t.Fatal("REP greeting", greeting, err)
	}
	if _, err := c.Write([]byte{0, 'S', 'P', 0, 0, 0x30, 0, 0}); err != nil {
		t.Fatal(err)
	}
}
func nngRequest(t *testing.T, c net.Conn, token string) LocalREPResponse {
	t.Helper()
	wire, err := nngwire.Reply(0x80000001, []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Write(wire); err != nil {
		t.Fatal(err)
	}
	body, err := nngwire.ReadRequest(c)
	if err != nil {
		t.Fatal(err)
	}
	if body.Correlation != 0x80000001 {
		t.Fatal("wire correlation", body.Correlation)
	}
	var response LocalREPResponse
	if err = json.Unmarshal(body.Body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
func TestNNGEncryptedSendReplayAndRefusals(t *testing.T) {
	f := newMQTTSNFixture(t)
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	defer client.Close()
	requests := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { serveNNGConnection(ctx, server, f.d, requests); close(done) }()
	nngClient(t, client)
	token := f.token(t, mqttSNRequest(), true, 0)
	for range 2 {
		response := nngRequest(t, client, token)
		if response.Error != nil || response.Result == nil || response.RequestID != mqttSNRequest().RequestID || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatalf("send %+v", response)
		}
	}
	job, err := f.s.Lookup(t.Context(), f.identity, mqttSNRequest().RequestID)
	if err != nil || job.Request.Message != mqttSNRequest().Message || job.Binding != "native" {
		t.Fatalf("job %+v %v", job, err)
	}
	for _, token := range []string{f.token(t, mqttSNRequest(), false, 0), f.token(t, mqttSNRequest(), true, -1000), "malformed"} {
		response := nngRequest(t, client, token)
		if response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil || response.RequestID != "" {
			t.Fatalf("refusal %+v", response)
		}
	}
	requests <- struct{}{}
	response := nngRequest(t, client, token)
	if response.Error == nil || response.Error.Code != core.CapacityExceeded || len(requests) != 1 {
		t.Fatalf("capacity %+v", response)
	}
	<-requests
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection not joined")
	}
	if len(requests) != 0 {
		t.Fatal("request slot leaked")
	}
}
func TestNNGMalformedRequestNeverAdmits(t *testing.T) {
	f := newMQTTSNFixture(t)
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { serveNNGConnection(t.Context(), server, f.d, make(chan struct{}, 1)); close(done) }()
	nngClient(t, client)
	if _, err := client.Write([]byte{0, 0, 0, 0, 0, 0, 0, 5, 0, 0, 0, 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nonterminal request retained owner")
	}
	if _, err := f.s.Lookup(t.Context(), f.identity, mqttSNRequest().RequestID); err == nil {
		t.Fatal("malformed request admitted")
	}
}
