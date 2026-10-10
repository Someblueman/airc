//go:build darwin || linux

package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/natswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestNATSServiceRawTLSDispatchAndJoinedShutdown(t *testing.T) {
	c, cert, signer, pub := natsServiceConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
	t.Cleanup(func() { cancel(); _ = reader.Close(); <-finished })
	var ready struct{ Listeners []struct{ Address string } }
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 1 {
		t.Fatal(ready, err)
	}
	address := ready.Listeners[0].Address
	raw, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	info, err := natswire.Read(bufio.NewReader(raw))
	if err != nil || info.Command != "INFO" {
		t.Fatal("raw INFO before TLS", err)
	}
	security := cert.Client.Clone()
	security.ServerName = "localhost"
	client := tls.Client(raw, security)
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	connect, err := json.Marshal(map[string]any{"auth_token": token, "verbose": true, "protocol": 1})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := natswire.Encode(natswire.Frame{Command: "CONNECT", JSON: connect})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(append(wire, []byte("SUB response 1\r\n")...)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(client)
	for range 2 {
		if reply, err := natswire.Read(r); err != nil || reply.Command != "+OK" {
			t.Fatal("CONNECT/SUB", err)
		}
	}
	// One shared service slot must be acquired exactly once by the NATS owner.
	second, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := natswire.Read(bufio.NewReader(second)); err == nil {
		t.Fatal("overcapacity received INFO")
	}
	request, wire := natsServicePublication(t, c, signer)
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	if ack, err := natswire.Read(r); err != nil || ack.Command != "+OK" {
		t.Fatal("PUB", err)
	}
	message, err := natswire.Read(r)
	if err != nil || message.Command != "MSG" || message.Subject != "response" || message.SID != "1" {
		t.Fatal("signed service response", err)
	}
	sum := sha256.Sum256([]byte("nats-core-response\x00request\x00response\x00room\x00#fixture"))
	scope := auth.ResponseScope{Realm: c.Realm, Principal: c.Principals[0].ID, Binding: c.Principals[0].Binding, Route: hex.EncodeToString(sum[:])}
	plain, err := auth.OpenSignedResponse(string(message.Payload), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var result adapter.NATSResult
	if err := json.Unmarshal(plain, &result); err != nil {
		t.Fatal(err)
	}
	if result.RequestID != request.RequestID || result.Result == nil || !result.Result.Result.Durable || result.Error != nil {
		t.Fatal("service admission result")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not join")
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("service retained listener", err)
	}
	_ = rebound.Close()
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("service retained journal", err)
	}
	defer journal.Close()
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	id, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	job, err := journal.Lookup(t.Context(), id, request.RequestID)
	if err != nil || job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target {
		t.Fatal("persisted service intent changed", err)
	}
}
