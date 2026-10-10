//go:build darwin || linux

package adapter

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/zmqwire"
)

func TestZeroMQConflictAndDurableRevocation(t *testing.T) {
	f := newMQTTSNFixture(t)
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { serveZeroMQConnection(ctx, server, f.d, make(chan struct{}, 1)); close(done) }()
	defer func() { cancel(); <-done }()
	zeroMQClient(t, client)
	request := mqttSNRequest()
	token := f.token(t, request, true, 0)
	if response := zeroMQRequest(t, client, token); response.Error != nil {
		t.Fatal(response.Error)
	}
	request.Message = "changed body"
	if response := zeroMQRequest(t, client, f.token(t, request, true, 0)); response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
		t.Fatalf("conflict %+v", response)
	}
	if _, err := f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if response := zeroMQRequest(t, client, token); response.Error == nil || response.Error.Code != core.Forbidden || response.Result != nil {
		t.Fatalf("revoked %+v", response)
	}
}

func TestZeroMQJoinedBlockedHandshakeAndReply(t *testing.T) {
	for _, reply := range []bool{false, true} {
		t.Run(map[bool]string{false: "greeting", true: "reply"}[reply], func(t *testing.T) {
			f := newMQTTSNFixture(t)
			server, client := net.Pipe()
			defer client.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			requests := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() { serveZeroMQConnection(ctx, server, f.d, requests); close(done) }()
			if reply {
				zeroMQClient(t, client)
				wire, err := zmqwire.Reply([]byte(f.token(t, mqttSNRequest(), true, 0)))
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
