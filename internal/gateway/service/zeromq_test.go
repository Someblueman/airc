//go:build darwin || linux

package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/zmqwire"
)

func TestZeroMQServiceEncryptedDispatchCapacityAndJoinedShutdown(t *testing.T) {
	c, signer, encrypter := zeroMQServiceConfig(t)
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
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 1 || ready.Listeners[0].ID != "zeromq" {
		t.Fatal("ready", ready, err)
	}
	address := ready.Listeners[0].Address
	client, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if err = zmqwire.ReadGreeting(client); err != nil {
		t.Fatal(err)
	}
	greeting := zmqwire.Greeting()
	peerReady := zmqwire.Ready()
	peerReady[len(peerReady)-1] = 'Q'
	if _, err = client.Write(append(greeting[:], peerReady...)); err != nil {
		t.Fatal(err)
	}
	if f, err := zmqwire.ReadFrame(client); err != nil || !f.Command {
		t.Fatal("REP READY", err)
	}
	second, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetDeadline(time.Now().Add(time.Second))
	if err = zmqwire.ReadGreeting(second); err == nil {
		t.Fatal("session cap bypassed")
	}
	issued, expires := time.Now().Unix(), time.Now().Unix()+300
	request := core.Request{Version: 1, RequestID: "zmq-service", Operation: core.Send, Target: &c.Principals[0].Targets[0], Message: "exact 世界\nwhole body", IssuedAt: &issued, ExpiresAt: &expires}
	body, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: c.Realm, IssuedAt: issued, ExpiresAt: expires, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encrypter.Encrypt([]byte(token))
	if err != nil {
		t.Fatal(err)
	}
	token, err = encrypted.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		wire, err := zmqwire.Reply([]byte(token))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.Write(wire); err != nil {
			t.Fatal(err)
		}
		reply, err := zmqwire.ReadRequest(client)
		if err != nil {
			t.Fatal(err)
		}
		var result adapter.LocalREPResponse
		if err = json.Unmarshal(reply, &result); err != nil || result.Error != nil || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable || result.RequestID != request.RequestID {
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
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := v.AuthenticateEncrypted(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job, err := journal.Lookup(t.Context(), verified.Identity, request.RequestID)
	if err != nil || job.Request.Message != request.Message || job.Binding != c.Principals[0].Binding {
		t.Fatal("original intent", job, err)
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("TCP not released", err)
	}
	_ = rebound.Close()
}
