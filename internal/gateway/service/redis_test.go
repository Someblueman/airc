//go:build darwin || linux

package service

import (
	"bufio"
	"bytes"
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
	"github.com/Someblueman/airc/internal/gateway/natswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisServiceOutboundTLSDispatchAndJoinedShutdown(t *testing.T) {
	c, cert, signer, pub := natsServiceConfig(t)
	c.Listeners, c.Limits.Sessions = []config.Listener{}, 2
	c.Principals[0].BearerTokenFile = ""
	request, wire := natsServicePublication(t, c, signer)
	publication, err := natswire.Read(bufio.NewReader(bytes.NewReader(wire)))
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
	c.RedisPubSub = []config.RedisPubSub{{ID: "redis", Enabled: true, Address: broker.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath,
		Subscriber: config.RedisCredential{Username: "reader", PasswordFile: password("reader-password")}, Publisher: config.RedisCredential{Username: "writer", PasswordFile: password("writer-password")},
		Routes: []config.RedisRoute{{Principal: c.Principals[0].ID, RequestChannel: "request", ResponseChannel: "response", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results, peersDone := make(chan []byte, 1), make(chan struct{})
	var peers sync.WaitGroup
	go func() {
		defer close(peersDone)
		defer peers.Wait()
		for i := range 2 {
			raw, err := broker.Accept()
			if err != nil {
				return
			}
			peers.Go(func() { redisServicePeer(t, ctx, i, raw, cert, results, publication.Payload) })
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
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || len(ready.Listeners) != 0 || len(ready.Brokers) != 1 || ready.Brokers[0].ID != "redis" || ready.Brokers[0].Protocol != "redis/pubsub" {
		t.Fatal("ready", err)
	}
	var token []byte
	select {
	case token = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("result missing")
	}
	sum := sha256.Sum256([]byte("redis-pubsub-response\x00request\x00response\x00" + request.Target.Kind + "\x00" + request.Target.Name))
	scope := auth.ResponseScope{Realm: c.Realm, Principal: c.Principals[0].ID, Binding: c.Principals[0].Binding, Route: hex.EncodeToString(sum[:])}
	plain, err := auth.OpenSignedResponse(string(token), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.RedisResult
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
	verified, err := v.AuthenticateSigned(string(publication.Payload), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job, err := journal.Lookup(t.Context(), verified.Identity, request.RequestID)
	if err != nil || job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target {
		t.Fatal("journal body/target", err)
	}
}
