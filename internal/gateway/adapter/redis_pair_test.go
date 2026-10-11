package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisPairAuthenticatedMessageAndPublish(t *testing.T) {
	publication := make(chan rediswire.Value, 1)
	b, security, count := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !redisPeerAuth(t, i, c, r) {
			return
		}
		v, err := rediswire.Read(r)
		if err != nil {
			return
		}
		if i == 0 {
			publication <- v
			_ = redisPeerWrite(c, rediswire.Value{Type: rediswire.Integer, Integer: 0})
		} else {
			if !reflect.DeepEqual(v, redisCommand([]byte("SUBSCRIBE"), []byte("request"))) {
				t.Error("subscription", v)
			}
			_ = redisPeerWrite(c, redisSubscribeReply("request", 1))
			_ = redisPeerWrite(c, redisCommand([]byte("message"), []byte("request"), []byte("body\r\n\x00é")))
		}
		_, _ = r.ReadByte()
	})
	slots := make(chan struct{}, 3)
	slots <- struct{}{} // Foreign connection capacity must survive close.
	p, err := openRedisPair(t.Context(), b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), slots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	if count.Load() != 2 || len(slots) != 3 {
		t.Fatal("socket pair admission")
	}
	b.Routes[0].ResponseChannel = "mutated"
	route, body, err := p.next()
	if err != nil || route.ResponseChannel != "response" || string(body) != "body\r\n\x00é" {
		t.Fatal("message or route snapshot", err)
	}
	if err := p.publish("response", []byte("plain result")); err != nil {
		t.Fatal(err)
	}
	if got := <-publication; !reflect.DeepEqual(got, redisCommand([]byte("PUBLISH"), []byte("response"), []byte("plain result"))) {
		t.Fatal("publication changed")
	}
	p.close()
	p.close()
	if len(slots) != 1 {
		t.Fatal("capacity leaked or foreign slot released")
	}
}

func TestRedisPairPartialCapacityAndTLSRefusal(t *testing.T) {
	b, security, count := redisFixture(t, func(_ int, c *tls.Conn, _ *bufio.Reader) { _ = c.Handshake() })
	slots := make(chan struct{}, 2)
	slots <- struct{}{}
	if p, err := openRedisPair(t.Context(), b, security, []byte("x"), []byte("x"), slots); err == nil || p != nil || len(slots) != 1 || count.Load() != 0 {
		t.Fatal("partial pair leaked or dialed")
	}
	<-slots
	bad := security.Clone()
	bad.InsecureSkipVerify = true
	if p, err := openRedisPair(t.Context(), b, bad, []byte("x"), []byte("x"), slots); err == nil || p != nil || len(slots) != 0 || count.Load() != 0 {
		t.Fatal("insecure TLS accepted")
	}
	bad = security.Clone()
	bad.RootCAs = nil
	if p, err := openRedisPair(t.Context(), b, bad, []byte("x"), []byte("x"), slots); err == nil || p != nil || len(slots) != 0 {
		t.Fatal("untrusted certificate accepted or slot leaked")
	}
}

func TestRedisPairIdleCancellation(t *testing.T) {
	b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !redisPeerAuth(t, i, c, r) {
			return
		}
		if i == 1 {
			if _, err := rediswire.Read(r); err != nil {
				return
			}
			_ = redisPeerWrite(c, redisSubscribeReply("request", 1))
		}
		_, _ = r.ReadByte()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots := make(chan struct{}, 2)
	p, err := openRedisPair(ctx, b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), slots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	done := make(chan error, 1)
	go func() { _, _, err := p.next(); done <- err }()
	select {
	case <-done:
		t.Fatal("quiet channel treated as failed")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("idle cancellation did not join")
	}
	if len(slots) != 0 {
		t.Fatal("capacity leaked")
	}
}
