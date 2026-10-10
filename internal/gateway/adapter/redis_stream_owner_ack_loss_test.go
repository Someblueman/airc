//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"
)

func TestRedisStreamsOwnerLostAppliedAckDoesNotReadmit(t *testing.T) {
	f := newRedisFixture(t)
	broker := redisStreamBroker()
	request := redisRequest()
	entry := redisStreamEntry(t, request)
	results := make(chan []byte, 1)
	recovered := make(chan struct{})
	b, security, count := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
			return
		}
		_, _ = c.Write([]byte("+OK\r\n"))
		if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", "0-0") {
			return
		}
		if i == 0 {
			_ = streamPeerEntry(c, broker.Route.Stream, entry)
			published := streamPeerResult(t, c, r, broker)
			if published == nil {
				return
			}
			results <- published
			_, _ = c.Write([]byte("$3\r\n2-0\r\n"))
			if !streamPeerCommand(t, r, "XACK", "events", "gateway", "1-0") {
				return
			}
			// The broker removes the pending entry but loses the XACK reply.
			return
		}
		_, _ = c.Write([]byte("*-1\r\n"))
		if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "BLOCK", "1000", "STREAMS", "events", ">") {
			return
		}
		close(recovered)
		_, _ = r.ReadByte()
	})
	broker.Address, broker.TLSServerName, broker.Credential = b.Address, b.TLSServerName, b.Subscriber
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeRedisStreams(ctx, broker, security, []byte("password"), f.d, slots, requests)
	}()
	select {
	case <-recovered:
	case <-time.After(3 * time.Second):
		t.Fatal("applied ACK recovery missing")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK owner failed to join")
	}
	response := streamResult(t, broker, entry.ID, <-results)
	job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || job.Result.RequestID != request.RequestID || job.Request.Message != request.Message || job.Result.SendAttempts != 0 || count.Load() != 2 || len(slots) != 0 || len(requests) != 0 {
		t.Fatal("applied ACK changed intent or capacity", err)
	}
}
