//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisReconnectAuthenticationFailureBackoffAndJoin(t *testing.T) {
	f := newRedisFixture(t)
	attempts := make(chan time.Time, 3)
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		v, err := rediswire.Read(r)
		if err != nil || len(v.Array) != 3 || string(v.Array[0].Bytes) != "AUTH" || string(v.Array[1].Bytes) != "writer" {
			t.Error("reconnect authentication", err)
			return
		}
		attempts <- time.Now()
		_ = redisPeerWrite(c, rediswire.Value{Type: rediswire.Error, Bytes: []byte("private diagnostic")})
		_, _ = r.ReadByte()
	})
	b.Routes = []config.RedisRoute{redisAdmissionRoute()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots, requests := make(chan struct{}, 2), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeRedisPubSub(ctx, b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), f.d, slots, requests)
	}()
	var previous time.Time
	for i := range 3 {
		select {
		case attempt := <-attempts:
			if i > 0 && attempt.Sub(previous) < redisBackoffMin*time.Duration(1<<(i-1)) {
				t.Fatal("authentication failure skipped backoff")
			}
			previous = attempt
		case <-time.After(2 * time.Second):
			t.Fatal("reconnect attempt missing")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("backoff cancellation did not join")
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("failed reconnect leaked capacity")
	}
	// Exercise cancellation of the maximum timer without waiting for its expiry.
	if redisWait(ctx, redisBackoffMax) {
		t.Fatal("canceled backoff completed")
	}
}
