//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisStreamsOwnerRefusedPendingDoesNotStarve(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	broker := redisStreamBroker(f)
	request := redisStreamRequest(t, broker, amqpRequest(), "3-0")
	valid := redisStreamEntry(t, f, request, 0)
	valid.ID = "3-0"
	finished := make(chan struct{})
	results := make(chan []byte, 1)
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
			return
		}
		_, _ = c.Write([]byte("+OK\r\n"))
		for i, cursor := range []string{"0-0", "1-0", "2-0", "0-0"} {
			if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", cursor) {
				return
			}
			switch i {
			case 0:
				_ = streamPeerEntry(c, broker.Route.Stream, rediswire.StreamEntry{ID: "1-0", Payload: []byte("unsigned")})
			case 1:
				_ = streamPeerEntry(c, broker.Route.Stream, rediswire.StreamEntry{ID: "2-0", Deleted: true})
			case 2:
				_, _ = c.Write([]byte("*-1\r\n"))
			case 3:
				close(finished)
				_, _ = r.ReadByte()
				return
			}
			if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "BLOCK", "1000", "STREAMS", "events", ">") {
				return
			}
			if i != 0 {
				_, _ = c.Write([]byte("*-1\r\n"))
				continue
			}
			_ = streamPeerEntry(c, broker.Route.Stream, valid)
			token := streamPeerResult(t, c, r, broker)
			if token == nil {
				return
			}
			results <- token
			_, _ = c.Write([]byte("$3\r\n4-0\r\n"))
			if !streamPeerCommand(t, r, "XACK", "events", "gateway", "3-0") {
				return
			}
			_, _ = c.Write([]byte(":1\r\n"))
		}
	})
	broker.Address, broker.TLSServerName, broker.Credential = b.Address, b.TLSServerName, b.Subscriber
	sealer, pub, _, _ := redisResultFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeRedisStreams(ctx, broker, security, []byte("password"), f.c.Realm, f.d, sealer, slots, requests)
	}()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("pending entries starved new intake or scan reset")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("owner failed to join")
	}
	result := streamOpenResult(t, f, broker, pub, valid.ID, <-results)
	if result.Error != nil || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable {
		t.Fatal("new intake missing")
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("owner leaked capacity")
	}
}
