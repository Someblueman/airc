package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/testtls"
)

func redisFixture(t *testing.T, serve func(int, *tls.Conn, *bufio.Reader)) (config.RedisPubSub, *tls.Config, *atomic.Int32) {
	t.Helper()
	cert := testtls.New(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	done := make(chan struct{})
	count := new(atomic.Int32)
	go func() {
		defer close(done)
		defer workers.Wait()
		for {
			raw, err := l.Accept()
			if err != nil {
				return
			}
			i := int(count.Add(1)) - 1
			workers.Go(func() {
				closed := make(chan struct{})
				stop := context.AfterFunc(ctx, func() { _ = raw.Close(); close(closed) })
				defer func() {
					if !stop() {
						<-closed
					}
				}()
				defer raw.Close()
				c := tls.Server(raw, cert.Server)
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				serve(i, c, bufio.NewReader(c))
			})
		}
	}()
	t.Cleanup(func() { cancel(); _ = l.Close(); <-done })
	b := config.RedisPubSub{ID: "fixture", Address: l.Addr().String(), TLSServerName: "localhost",
		Subscriber: config.RedisCredential{Username: "reader"}, Publisher: config.RedisCredential{Username: "writer"},
		Routes: []config.RedisRoute{{Principal: "producer", RequestChannel: "request", ResponseChannel: "response"}}}
	cert.Client.ServerName = "localhost"
	return b, cert.Client, count
}

func redisPeerWrite(c *tls.Conn, v rediswire.Value) error {
	return redisWrite(c, v, time.Now().Add(5*time.Second))
}

func redisPeerAuth(t *testing.T, index int, c *tls.Conn, r *bufio.Reader) bool {
	t.Helper()
	v, err := rediswire.Read(r)
	username := "writer"
	if index%2 == 1 {
		username = "reader"
	}
	if err != nil || v.Type != rediswire.Array || len(v.Array) != 3 || string(v.Array[0].Bytes) != "AUTH" || string(v.Array[1].Bytes) != username || string(v.Array[2].Bytes) != "password\r\n\x00" {
		t.Error("incorrect broker authentication command", err)
		return false
	}
	return redisPeerWrite(c, rediswire.Value{Type: rediswire.Simple, Bytes: []byte("OK")}) == nil
}

func redisSubscribeReply(channel string, count int64) rediswire.Value {
	v := redisCommand([]byte("subscribe"), []byte(channel), nil)
	v.Array[2] = rediswire.Value{Type: rediswire.Integer, Integer: count}
	return v
}
