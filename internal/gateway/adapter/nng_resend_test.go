//go:build darwin || linux

package adapter

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNNGResendAfterReconnectRetainsSignedIntent(t *testing.T) {
	f := newMQTTSNFixture(t)
	request := mqttSNRequest()
	token := f.token(t, request, true, 0)
	var upstreamID string
	for range 2 {
		server, client := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { serveNNGConnection(ctx, server, f.d, make(chan struct{}, 1)); close(done) }()
		func() {
			defer func() { cancel(); _ = client.Close(); <-done }()
			nngClient(t, client)
			reply := nngRequest(t, client, token)
			if reply.Error != nil || reply.RequestID != request.RequestID || reply.Result == nil || reply.Result.Result == nil || !reply.Result.Result.Durable {
				t.Fatalf("resend %+v", reply)
			}
		}()
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
		if err != nil || job.Request.Message != request.Message || job.Binding != "native" {
			t.Fatalf("intent %+v %v", job, err)
		}
		if upstreamID != "" && job.UpstreamID != upstreamID {
			t.Fatal("resend replaced durable job")
		}
		upstreamID = job.UpstreamID
	}
	if upstreamID == "" {
		t.Fatal("missing durable identity")
	}
}

func TestNNGJoinedPartialPeerHandshake(t *testing.T) {
	f := newMQTTSNFixture(t)
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { serveNNGConnection(ctx, server, f.d, make(chan struct{}, 1)); close(done) }()
	var greeting [8]byte
	if _, err := client.Read(greeting[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte{0, 'S'}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("partial handshake not joined")
	}
}
