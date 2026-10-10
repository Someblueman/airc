//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamsOwnerExpiredPendingRecovery(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	broker := redisStreamBroker(f)
	request := redisStreamRequest(t, broker, amqpRequest(), "1-0")
	entry := redisStreamEntry(t, f, request, -400)
	old := time.Now().Add(-400 * time.Second)
	message, err := f.v.AuthenticateSigned(string(entry.Payload), old)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := f.s.Admit(t.Context(), message.Identity, message.Request, message.ReplayUntil, old)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.s, err = store.Open(t.Context(), f.c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.Close() })
	f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, broker.Route.Operations)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan []byte, 1)
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
			return
		}
		_, _ = c.Write([]byte("+OK\r\n"))
		token := streamPeerResult(t, c, r, broker)
		if token == nil {
			return
		}
		results <- token
		_, _ = c.Write([]byte("$3\r\n2-0\r\n"))
		if !streamPeerCommand(t, r, "XACK", "events", "gateway", "1-0") {
			return
		}
		_, _ = c.Write([]byte(":1\r\n"))
		_, _ = r.ReadByte()
	})
	broker.Address, broker.TLSServerName, broker.Credential = b.Address, b.TLSServerName, b.Subscriber
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	p, err := openRedisStream(t.Context(), broker, security, []byte("password"), slots)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	sealer, pub, _, _ := redisResultFixture(t)
	completed, err := p.completeStreamEntry(f.c.Realm, f.d, sealer, entry, requests)
	if err != nil || !completed {
		t.Fatal("expired retained result not completed", err)
	}
	result := streamOpenResult(t, f, broker, pub, entry.ID, <-results)
	if result.Result == nil || result.Result.Result == nil || !reflect.DeepEqual(*result.Result.Result, before.Result) {
		t.Fatal("retained result changed")
	}
	after, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("retained recovery mutated job", err)
	}
	p.close()
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("retained recovery leaked capacity")
	}
}
