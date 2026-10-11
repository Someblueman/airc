//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/zmqwire"
)

func zeroMQClient(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := zmqwire.ReadGreeting(c); err != nil {
		t.Fatal(err)
	}
	greeting := zmqwire.Greeting()
	ready := zmqwire.Ready()
	ready[len(ready)-1] = 'Q'
	if _, err := c.Write(append(greeting[:], ready...)); err != nil {
		t.Fatal(err)
	}
	f, err := zmqwire.ReadFrame(c)
	if err != nil || !f.Command || !bytes.Contains(f.Body, []byte("REP")) {
		t.Fatal("server READY", err)
	}
}
func zeroMQRequest(t *testing.T, c net.Conn, payload string) LocalREPResponse {
	t.Helper()
	wire, err := zmqwire.Reply([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Write(wire); err != nil {
		t.Fatal(err)
	}
	body, err := zmqwire.ReadRequest(c)
	if err != nil {
		t.Fatal(err)
	}
	var response LocalREPResponse
	if err = json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
func TestZeroMQSendReplayAndRefusals(t *testing.T) {
	f := newLocalREPFixture(t, "zeromq")
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	defer client.Close()
	requests := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { serveLocalREPConnection(ctx, server, f.d, requests, zeroMQREP); close(done) }()
	zeroMQClient(t, client)
	payload := f.body(t, mqttSNRequest())
	for range 2 {
		response := zeroMQRequest(t, client, payload)
		if response.Error != nil || response.Result == nil || response.RequestID != mqttSNRequest().RequestID || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatalf("send %+v", response)
		}
	}
	job, err := f.lookup(t, mqttSNRequest().RequestID)
	if err != nil || job.Request.Message != mqttSNRequest().Message {
		t.Fatalf("job %+v %v", job, err)
	}
	for _, body := range []string{"malformed", `{"version":1,"request_id":"no-author","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x"}`} {
		response := zeroMQRequest(t, client, body)
		if response.Error == nil || response.Result != nil {
			t.Fatalf("refusal %+v", response)
		}
	}
	requests <- struct{}{}
	response := zeroMQRequest(t, client, payload)
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
func TestZeroMQMalformedRequestNeverAdmits(t *testing.T) {
	f := newLocalREPFixture(t, "zeromq")
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() {
		serveLocalREPConnection(t.Context(), server, f.d, make(chan struct{}, 1), zeroMQREP)
		close(done)
	}()
	zeroMQClient(t, client)
	if _, err := client.Write([]byte{1, 0, 1, 1, 'x'}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("multipart request retained owner")
	}
	if _, err := f.lookup(t, mqttSNRequest().RequestID); err == nil {
		t.Fatal("malformed request admitted")
	}
}
