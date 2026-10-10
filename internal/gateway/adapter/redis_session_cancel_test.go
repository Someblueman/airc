//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisSessionShutdownCancelsActiveNativeRead(t *testing.T) {
	f := newRedisFixture(t)
	native, err := net.Listen("unix", f.c.Bindings[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	accepted, finished := make(chan net.Conn, 1), make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := native.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = native.Close()
		<-finished
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})
	route := redisAdmissionRoute()
	request := core.Request{Version: 1, RequestID: "redis-active-read", Operation: core.HistoryRead, Target: redisRequest().Target, Limit: 1}
	payload := redisPlainRequest(t, request)
	b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !redisPeerAuth(t, i, c, r) {
			return
		}
		if i == 1 {
			if _, err := rediswire.Read(r); err != nil {
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
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots, requests := make(chan struct{}, 2), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeRedisPubSub(ctx, b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), f.d, slots, requests)
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("native read not started")
	}
	defer conn.Close()
	if len(requests) != 1 {
		t.Fatal("native read did not claim capacity")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("active dispatch did not join")
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("capacity leaked")
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatal("native read socket retained", err)
	}
}
