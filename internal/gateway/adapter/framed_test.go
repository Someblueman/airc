package adapter

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestFramedGlobalCapacityAndJoinedCancellation(t *testing.T) {
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	done := make(chan struct{})
	go func() { serveFramedConnection(ctx, server, nil, requests, false); close(done) }()
	body := []byte(`{"version":1,"correlation_id":"full","profile":"signed","envelope":"unused"}`)
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(body)))
	sent := make(chan error, 1)
	go func() { _, err := client.Write(append(h[:], body...)); sent <- err }()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(client, h[:]); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, binary.BigEndian.Uint32(h[:]))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != `{"version":1,"correlation_id":"full","event":"error","error":{"code":"capacity_exceeded","transient":true}}` {
		t.Fatal("capacity admitted a request", string(response))
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection cancellation not joined")
	}
	_ = client.Close()
	if len(requests) != 1 {
		t.Fatal("rejection consumed or released someone else's request slot")
	}
}
