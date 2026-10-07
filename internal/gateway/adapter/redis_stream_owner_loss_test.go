//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamsOwnerLossRecovery(t *testing.T) {
	for _, loss := range []string{"entry", "publication", "acknowledgement"} {
		t.Run(loss, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			broker := redisStreamBroker(f)
			request := redisStreamRequest(t, broker, amqpRequest(), "1-0")
			entry := redisStreamEntry(t, f, request, 0)
			lost := make(chan time.Time, 1)
			restored := make(chan time.Time, 1)
			results := make(chan []byte, 2)
			acknowledged := make(chan struct{})
			b, security, count := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
				if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
					return
				}
				_, _ = c.Write([]byte("+OK\r\n"))
				if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", "0-0") {
					return
				}
				if i == 0 && loss == "entry" {
					// Entry is pending at the broker but its incomplete frame cannot admit.
					_, _ = c.Write([]byte("*1\r\n*2\r\n$6\r\nevents\r\n"))
					lost <- time.Now()
					return
				}
				if i == 0 {
					_, _ = c.Write([]byte("*-1\r\n"))
					if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "BLOCK", "1000", "STREAMS", "events", ">") {
						return
					}
				} else {
					restored <- time.Now()
				}
				if streamPeerEntry(c, broker.Route.Stream, entry) != nil {
					return
				}
				token := streamPeerResult(t, c, r, broker)
				if token == nil {
					return
				}
				results <- token
				if i == 0 && loss == "publication" {
					lost <- time.Now()
					return
				}
				_, _ = c.Write([]byte("$3\r\n2-0\r\n"))
				if !streamPeerCommand(t, r, "XACK", "events", "gateway", "1-0") {
					return
				}
				if i == 0 {
					lost <- time.Now()
					return
				}
				_, _ = c.Write([]byte(":1\r\n"))
				close(acknowledged)
				_, _ = r.ReadByte()
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
			var firstTime time.Time
			select {
			case firstTime = <-lost:
			case <-time.After(3 * time.Second):
				t.Fatal("loss boundary missing")
			}
			first, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
			if loss == "entry" {
				if err != store.ErrNotFound {
					t.Fatal("partial entry admitted", err)
				}
			} else if err != nil || !first.Result.Durable {
				t.Fatal("publication before durable intake", err)
			}
			select {
			case <-acknowledged:
			case <-time.After(3 * time.Second):
				t.Fatal("pending recovery missing")
			}
			if second := <-restored; second.Sub(firstTime) < redisBackoffMin {
				t.Fatal("reconnect omitted backoff")
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
			for len(results) > 0 {
				result := streamOpenResult(t, f, broker, pub, entry.ID, <-results)
				if result.Error != nil || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable || result.Result.Result.RequestID != request.RequestID {
					t.Fatal("durable result changed")
				}
			}
			last, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
			if err != nil || last.Request.Message != request.Message || last.Result.SendAttempts != 0 || (loss != "entry" && last.UpstreamID != first.UpstreamID) || count.Load() != 2 || len(slots) != 0 || len(requests) != 0 {
				t.Fatal("identity changed or capacity leaked", err)
			}
			// No worker/native observer is running: these controls prove durable intake
			// identity and ordering, not native acceptance.
		})
	}
}
