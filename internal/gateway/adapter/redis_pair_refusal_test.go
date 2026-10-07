package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisPairSetupRefusals(t *testing.T) {
	for _, mode := range []string{"auth", "count", "channel", "duplicate", "null", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
				if i == 1 && (mode == "auth" || mode == "deadline") {
					if _, err := rediswire.Read(r); err != nil {
						return
					}
					if mode == "auth" {
						_ = redisPeerWrite(c, rediswire.Value{Type: rediswire.Error, Bytes: []byte("secret broker diagnostic")})
					}
					_, _ = r.ReadByte()
					return
				}
				if !redisPeerAuth(t, i, c, r) {
					return
				}
				if i == 1 {
					if _, err := rediswire.Read(r); err != nil {
						return
					}
					v := redisSubscribeReply("request", 1)
					switch mode {
					case "count":
						v.Array[2].Integer = 0
					case "channel":
						v.Array[1].Bytes = []byte("other")
					case "null":
						v.Array[1] = rediswire.Value{Type: rediswire.Bulk, Null: true}
					case "duplicate":
						_ = redisPeerWrite(c, v)
						v.Array[2].Integer = 2
					}
					_ = redisPeerWrite(c, v)
				}
				_, _ = r.ReadByte()
			})
			if mode == "duplicate" {
				r := b.Routes[0]
				r.RequestChannel, r.ResponseChannel = "second", "second-response"
				b.Routes = append(b.Routes, r)
			}
			ctx := t.Context()
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			slots := make(chan struct{}, 2)
			p, err := openRedisPair(ctx, b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), slots)
			if p != nil || err != errRedisConnection || len(slots) != 0 {
				t.Fatal("setup refusal leaked pair or diagnostic", err)
			}
		})
	}
}

func TestRedisPairMessageRefusalsAndFrameDeadline(t *testing.T) {
	for _, mode := range []string{"foreign", "null", "ack", "slow_frame"} {
		t.Run(mode, func(t *testing.T) {
			b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
				if !redisPeerAuth(t, i, c, r) {
					return
				}
				if i == 1 {
					if _, err := rediswire.Read(r); err != nil {
						return
					}
					_ = redisPeerWrite(c, redisSubscribeReply("request", 1))
					v := redisCommand([]byte("message"), []byte("request"), []byte("body"))
					switch mode {
					case "foreign":
						v.Array[1].Bytes = []byte("other")
					case "null":
						v.Array[2] = rediswire.Value{Type: rediswire.Bulk, Null: true}
					case "ack":
						v = redisSubscribeReply("request", 1)
					case "slow_frame":
						_, _ = c.Write([]byte("*"))
					}
					if mode != "slow_frame" {
						_ = redisPeerWrite(c, v)
					}
				}
				_, _ = r.ReadByte()
			})
			slots := make(chan struct{}, 2)
			p, err := openRedisPair(t.Context(), b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), slots)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.close)
			started := time.Now()
			_, payload, err := p.next()
			if err != errRedisConnection || payload != nil || len(slots) != 0 {
				t.Fatal("message refusal failed")
			}
			if mode == "slow_frame" && (time.Since(started) < redisDeadline || time.Since(started) > redisDeadline+time.Second) {
				t.Fatal("frame deadline not bounded")
			}
		})
	}
}
