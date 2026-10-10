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
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisSessionSignedReplayConflictAndUnsignedRefusal(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	route := redisAdmissionRoute(f)
	request := amqpRequest()
	request.RequestID = "redis-session"
	valid := redisSignedRequest(t, f, request, 0)
	request.Message = "different intent"
	conflict := redisSignedRequest(t, f, request, 0)
	results := make(chan []byte, 3)
	b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !redisPeerAuth(t, i, c, r) {
			return
		}
		if i == 0 {
			for range 3 {
				v, err := rediswire.Read(r)
				if err != nil || v.Type != rediswire.Array || len(v.Array) != 3 || string(v.Array[0].Bytes) != "PUBLISH" || string(v.Array[1].Bytes) != route.ResponseChannel {
					t.Error("result publication", err)
					return
				}
				results <- v.Array[2].Bytes
				if redisPeerWrite(c, rediswire.Value{Type: rediswire.Integer, Integer: 1}) != nil {
					return
				}
			}
		} else {
			v, err := rediswire.Read(r)
			if err != nil || len(v.Array) != 2 || string(v.Array[1].Bytes) != route.RequestChannel {
				t.Error("subscription", err)
				return
			}
			if redisPeerWrite(c, redisSubscribeReply(route.RequestChannel, 1)) != nil {
				return
			}
			for _, payload := range [][]byte{[]byte("unsigned payload"), valid, valid, conflict} {
				if redisPeerWrite(c, redisCommand([]byte("message"), []byte(route.RequestChannel), payload)) != nil {
					return
				}
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
	for i := range 3 {
		var token []byte
		select {
		case token = <-results:
		case <-time.After(time.Second):
			t.Fatal("signed result missing")
		}
		plain, err := auth.OpenSignedResponse(string(token), "response", pub, redisResponseScope(f.c.Realm, route.Principal, f.identity.Binding(), route), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var result RedisResult
		if err := json.Unmarshal(plain, &result); err != nil {
			t.Fatal(err)
		}
		if result.RequestID != request.RequestID {
			t.Fatal("unauthorized correlation")
		}
		if i < 2 {
			if result.Error != nil || result.Result == nil || !result.Result.Result.Durable {
				t.Fatal("durable replay result")
			}
		} else if result.Result != nil || result.Error == nil || result.Error.Code != core.IDConflict || result.Error.Transient {
			t.Fatal("conflict result")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("owner did not join")
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("owner capacity leaked")
	}
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || job.Request.Message != amqpRequest().Message || job.Result.SendAttempts != 0 {
		t.Fatal("journal intent changed", err)
	}
}
