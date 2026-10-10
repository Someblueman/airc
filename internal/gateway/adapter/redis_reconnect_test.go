//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisReconnectLostResultReplaysDurableIntent(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	route := redisAdmissionRoute(f)
	request := amqpRequest()
	request.RequestID = "redis-lost-result"
	payload := redisSignedRequest(t, f, request, 0)
	results := make(chan []byte, 1)
	lost := make(chan time.Time, 1)
	reconnected := make(chan time.Time, 1)
	b, security, count := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !redisPeerAuth(t, i, c, r) {
			return
		}
		if i%2 == 0 {
			if i == 2 {
				reconnected <- time.Now()
			}
			v, err := rediswire.Read(r)
			if err != nil || len(v.Array) != 3 || string(v.Array[0].Bytes) != "PUBLISH" {
				t.Error("result publication", err)
				return
			}
			if i == 0 {
				lost <- time.Now()
				return
			} // Close before PUBLISH reply.
			results <- v.Array[2].Bytes
			if redisPeerWrite(c, rediswire.Value{Type: rediswire.Integer, Integer: 1}) != nil {
				return
			}
		} else {
			v, err := rediswire.Read(r)
			if err != nil || len(v.Array) != 2 || string(v.Array[1].Bytes) != route.RequestChannel {
				t.Error("resubscription", err)
				return
			}
			if redisPeerWrite(c, redisSubscribeReply(route.RequestChannel, 1)) != nil {
				return
			}
			if redisPeerWrite(c, redisCommand([]byte("message"), []byte(route.RequestChannel), payload)) != nil {
				return
			}
		}
		_, _ = r.ReadByte()
	})
	b.Routes = []config.RedisRoute{route}
	sealer, pub, _, _ := redisResultFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots, requests := make(chan struct{}, 2), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeRedisPubSub(ctx, b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), f.c.Realm, f.d, sealer, slots, requests)
	}()
	var firstTime time.Time
	select {
	case firstTime = <-lost:
	case <-time.After(time.Second):
		t.Fatal("lost result did not reach broker")
	}
	first, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || !first.Result.Durable {
		t.Fatal("lost result had no durable intake", err)
	}
	var token []byte
	select {
	case token = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("replayed result missing")
	}
	if secondTime := <-reconnected; secondTime.Sub(firstTime) < redisBackoffMin {
		t.Fatal("reconnect skipped backoff")
	}
	plain, err := auth.OpenSignedResponse(string(token), "response", pub, redisResponseScope(f.c.Realm, route.Principal, f.identity.Binding(), route), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var result RedisResult
	if err := json.Unmarshal(plain, &result); err != nil {
		t.Fatal(err)
	}
	if result.RequestID != request.RequestID || result.Error != nil || result.Result == nil || !result.Result.Result.Durable {
		t.Fatal("replay result changed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect owner did not join")
	}
	last, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || last.UpstreamID != first.UpstreamID || last.Request.Message != request.Message || last.Result.SendAttempts != 0 || count.Load() != 4 || len(slots) != 0 || len(requests) != 0 {
		t.Fatal("reconnect changed identity or leaked resources", err)
	}
}
