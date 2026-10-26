package adapter

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestFramedGlobalCapacityAndJoinedCancellation(t *testing.T) {
	f := newPlainFixture(t, config.Listener{ID: "framed", Protocol: "framed", Operations: []core.Operation{core.Send}})
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	done := make(chan struct{})
	go func() { serveFramedConnection(ctx, server, f.d, requests); close(done) }()
	body := []byte(`{"version":1,"correlation_id":"full","profile":"plain","request":` + f.body(t, framedTestRequest()) + `}`)
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
	if f.jobs(t, framedTestRequest().RequestID) != 0 {
		t.Fatal("capacity refusal reached intake")
	}
	if len(requests) != 1 {
		t.Fatal("rejection consumed or released someone else's request slot")
	}
}

// A response keeps its encoding only for the sequence it was encoded with.
func TestEncodeFrameReusesOnlyMatchingSequence(t *testing.T) {
	response := FrameResponse{Version: 1, CorrelationID: "c", Event: "end", Sequence: 2}
	fresh, err := encodeFrame(response)
	if err != nil {
		t.Fatal(err)
	}
	response.encoded, response.encodedSequence = []byte(`{"cached":true}`), 2
	if got, _ := encodeFrame(response); string(got) != `{"cached":true}` {
		t.Fatalf("cached encoding not reused: %s", got)
	}
	response.Sequence = 3
	got, err := encodeFrame(response)
	if err != nil || string(got) == `{"cached":true}` || !strings.Contains(string(got), `"sequence":3`) {
		t.Fatalf("stale encoding reused after renumbering: %s %v", got, err)
	}
	if strings.Contains(string(fresh), "encoded") {
		t.Fatalf("cache leaked into JSON: %s", fresh)
	}
}
