//go:build darwin || linux

package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/nngwire"
	"github.com/Someblueman/airc/gateway/internal/store"
)

func TestNNGServiceDispatchCapacityAndJoinedShutdown(t *testing.T) {
	c := zeroMQServiceConfig(t)
	c.Listeners[0].ID = "nng"
	c.Listeners[0].Protocol = "nng"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
	t.Cleanup(func() { cancel(); _ = reader.Close(); <-finished })
	var ready struct {
		Listeners []struct{ ID, Address string }
	}
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 1 || ready.Listeners[0].ID != "nng" {
		t.Fatal("ready", ready, err)
	}
	address := ready.Listeners[0].Address
	client, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	var greeting [8]byte
	if _, err = io.ReadFull(client, greeting[:]); err != nil || greeting != nngwire.Greeting() {
		t.Fatal("REP greeting", greeting, err)
	}
	if _, err = client.Write([]byte{0, 'S', 'P', 0, 0, 0x30, 0, 0}); err != nil {
		t.Fatal(err)
	}
	second, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetDeadline(time.Now().Add(time.Second))
	if err = nngwire.ReadGreeting(second); err == nil {
		t.Fatal("session cap bypassed")
	}
	request := core.Request{Version: 1, RequestID: "nng-service", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact 世界\nwhole body"}
	body := serviceRequest(t, request)
	for correlation := uint32(0x80000001); correlation <= 0x80000002; correlation++ {
		wire, err := nngwire.Reply(correlation, body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.Write(wire); err != nil {
			t.Fatal(err)
		}
		reply, err := nngwire.ReadRequest(client)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Correlation != correlation {
			t.Fatal("correlation", reply.Correlation)
		}
		var result adapter.LocalREPResponse
		if err = json.Unmarshal(reply.Body, &result); err != nil || result.Error != nil || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable || result.RequestID != request.RequestID {
			t.Fatal("send", result, err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service not joined")
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("journal not released", err)
	}
	defer journal.Close()
	job, err := serviceJob(t, journal, request.RequestID)
	if err != nil || job.Request.Message != request.Message {
		t.Fatal("original intent", job, err)
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("TCP not released", err)
	}
	_ = rebound.Close()
}
