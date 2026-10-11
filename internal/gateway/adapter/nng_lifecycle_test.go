//go:build darwin || linux

package adapter

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/nngwire"
)

func TestNNGConflictKeepsOriginalIntent(t *testing.T) {
	f := newLocalREPFixture(t, "nng")
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { serveNNGConnection(ctx, server, f.d, make(chan struct{}, 1)); close(done) }()
	defer func() { cancel(); <-done }()
	nngClient(t, client)
	request := mqttSNRequest()
	payload := f.body(t, request)
	if response := nngRequest(t, client, payload); response.Error != nil {
		t.Fatal(response.Error)
	}
	request.Message = "changed body"
	if response := nngRequest(t, client, f.body(t, request)); response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatalf("conflict %+v", response)
	}
}

func TestNNGJoinedBlockedHandshakeAndReply(t *testing.T) {
	for _, reply := range []bool{false, true} {
		t.Run(map[bool]string{false: "greeting", true: "reply"}[reply], func(t *testing.T) {
			f := newLocalREPFixture(t, "nng")
			server, client := net.Pipe()
			defer client.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			requests := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() { serveNNGConnection(ctx, server, f.d, requests); close(done) }()
			if reply {
				nngClient(t, client)
				wire, err := nngwire.Reply(0x80000002, []byte(f.body(t, mqttSNRequest())))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = client.Write(wire); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(time.Second)
				for len(requests) == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if len(requests) != 1 {
					t.Fatal("request not admitted")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("blocked owner not joined")
			}
			if len(requests) != 0 {
				t.Fatal("request slot leaked")
			}
		})
	}
}
