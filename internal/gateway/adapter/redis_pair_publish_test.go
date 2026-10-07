package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisPairPublishRefusalsAndCancellation(t *testing.T) {
	for _, mode := range []string{"negative_count", "wrong_type", "foreign", "oversized", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
				if !redisPeerAuth(t, i, c, r) {
					return
				}
				if i == 1 {
					if _, err := rediswire.Read(r); err != nil {
						return
					}
					_ = redisPeerWrite(c, redisSubscribeReply("request", 1))
				} else {
					v, err := rediswire.Read(r)
					if mode == "foreign" || mode == "oversized" {
						if err == nil {
							t.Error("invalid result was published")
						}
						return
					}
					if err != nil || v.Type != rediswire.Array || len(v.Array) != 3 || string(v.Array[0].Bytes) != "PUBLISH" {
						t.Error("result command", err)
						return
					}
					close(started)
					if mode == "negative_count" {
						_ = redisPeerWrite(c, rediswire.Value{Type: rediswire.Integer, Integer: -1})
					}
					if mode == "wrong_type" {
						_ = redisPeerWrite(c, rediswire.Value{Type: rediswire.Simple, Bytes: []byte("OK")})
					}
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
			channel, body := "response", []byte("result")
			if mode == "foreign" {
				channel = "other"
			}
			if mode == "oversized" {
				body = make([]byte, rediswire.MaxBulk+1)
			}
			done := make(chan error, 1)
			go func() { done <- p.publish(channel, body) }()
			if mode == "cancel" {
				select {
				case <-started:
					cancel()
				case <-time.After(time.Second):
					t.Fatal("publish did not start")
				}
			}
			select {
			case err := <-done:
				if err != errRedisConnection {
					t.Fatal("invalid result accepted", err)
				}
			case <-time.After(time.Second):
				t.Fatal("result failure did not join")
			}
			if len(slots) != 0 {
				t.Fatal("result failure leaked capacity")
			}
		})
	}
}
