//go:build darwin || linux

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamsServiceSignedDispatchAndJoinedShutdown(t *testing.T) {
	c, cert, signer, pub := natsServiceConfig(t)
	c.Listeners, c.Limits.Sessions = []config.Listener{}, 2
	c.Principals[0].BearerTokenFile = ""
	issued, expires := time.Now().Unix(), time.Now().Unix()+300
	requestID, err := config.RedisStreamRequestID("broker", "events", "1-0")
	if err != nil {
		t.Fatal(err)
	}
	request := core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: &c.Principals[0].Targets[0], Message: "service body 世界\nsecond line", IssuedAt: &issued, ExpiresAt: &expires}
	body, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: c.Realm, IssuedAt: issued, ExpiresAt: expires, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	broker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	password := func(name string) string {
		path := filepath.Join(filepath.Dir(c.StateDir), name)
		if err := os.WriteFile(path, []byte("broker password"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c.RedisStreams = []config.RedisStreams{{ID: "streams", Enabled: true, Namespace: "broker", Address: broker.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath,
		Credential: config.RedisCredential{Username: "reader", PasswordFile: password("reader-password")}, Group: "gateway", Consumer: "owner",
		Route: config.RedisStreamRoute{Principal: c.Principals[0].ID, Stream: "events", ResultStream: "results", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results, peersDone := make(chan []byte, 1), make(chan struct{})
	var peers sync.WaitGroup
	go func() {
		defer close(peersDone)
		defer peers.Wait()
		for range 1 {
			raw, err := broker.Accept()
			if err != nil {
				return
			}
			peers.Go(func() { redisStreamsServicePeer(t, raw, cert, results, []byte(payload)) })
		}
	}()
	t.Cleanup(func() { cancel(); _ = broker.Close(); <-peersDone })
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		err := Run(ctx, c, writer)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	t.Cleanup(func() { cancel(); _ = reader.Close(); <-finished })
	var ready struct {
		Listeners []json.RawMessage
		Brokers   []struct{ ID, Protocol string }
	}
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 0 || len(ready.Brokers) != 1 || ready.Brokers[0].ID != "streams" || ready.Brokers[0].Protocol != "redis/streams" {
		t.Fatal("ready", err)
	}
	var token []byte
	select {
	case token = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("result missing")
	}
	sum := sha256.Sum256([]byte("redis-stream-response\x00broker\x00events\x00results\x001-0\x00" + request.Target.Kind + "\x00" + request.Target.Name))
	scope := auth.ResponseScope{Realm: c.Realm, Principal: c.Principals[0].ID, Binding: c.Principals[0].Binding, Route: hex.EncodeToString(sum[:])}
	plain, err := auth.OpenSignedResponse(string(token), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.RedisStreamResult
	if err := json.Unmarshal(plain, &response); err != nil || response.RequestID != request.RequestID || response.Error != nil || response.Result == nil || !response.Result.Result.Durable {
		t.Fatal("signed result", err)
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
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := v.AuthenticateSigned(payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job, err := journal.Lookup(t.Context(), verified.Identity, request.RequestID)
	if err != nil || job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target {
		t.Fatal("journal body/target", err)
	}
}
