//go:build darwin || linux

package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRedisServiceOutboundTLSDispatchAndJoinedShutdown(t *testing.T) {
	c, cert := redisServiceConfig(t)
	request, payload := redisServiceRequest(c)
	broker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.RedisPubSub = []config.RedisPubSub{redisServicePubSub(t, c, cert, broker.Addr().String())}
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
			peers.Go(func() { redisServicePeer(t, ctx, i, raw, cert, results, payload) })
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
	var published []byte
	select {
	case published = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("result missing")
	}
	var response adapter.RedisResult
	if err := json.Unmarshal(published, &response); err != nil || response.Protocol != "redis/pubsub" || response.RequestID != request.RequestID || response.Target != *request.Target || response.Error != nil || response.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("plain result %s: %v", published, err)
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
	job := redisServiceJournal(t, c, request.RequestID)
	if job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target || job.Author != (core.Author{Nick: "redis-guest"}) {
		t.Fatal("journal body/target/author")
	}
}
