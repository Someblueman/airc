package adapter

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisPairBatchedSubscriptionsDiscardPreIntakePublication(t *testing.T) {
	b, security, _ := redisFixture(t, func(i int, c *tls.Conn, r *bufio.Reader) {
		if !redisPeerAuth(t, i, c, r) {
			return
		}
		if i == 1 {
			for batch := 0; batch < 2; batch++ {
				v, err := rediswire.Read(r)
				if err != nil || v.Type != rediswire.Array || len(v.Array) != 65 || string(v.Array[0].Bytes) != "SUBSCRIBE" {
					t.Error("bounded subscription batch", err)
					return
				}
				for n, channel := range v.Array[1:] {
					index := batch*64 + n
					if string(channel.Bytes) != fmt.Sprintf("request%d", index) {
						t.Error("subscription order")
						return
					}
					if redisPeerWrite(c, redisSubscribeReply(string(channel.Bytes), int64(index+1))) != nil {
						return
					}
					if index == 0 {
						if redisPeerWrite(c, redisCommand([]byte("message"), channel.Bytes, []byte("pre-intake loss"))) != nil {
							return
						}
					}
				}
			}
			_ = redisPeerWrite(c, redisCommand([]byte("message"), []byte("request127"), []byte("after setup")))
		}
		_, _ = r.ReadByte()
	})
	route := b.Routes[0]
	b.Routes = nil
	for i := range 128 {
		route.RequestChannel, route.ResponseChannel = fmt.Sprintf("request%d", i), fmt.Sprintf("response%d", i)
		b.Routes = append(b.Routes, route)
	}
	slots := make(chan struct{}, 2)
	p, err := openRedisPair(t.Context(), b, security, []byte("password\r\n\x00"), []byte("password\r\n\x00"), slots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	got, payload, err := p.next()
	if err != nil || got.RequestChannel != "request127" || string(payload) != "after setup" {
		t.Fatal("pre-intake publication queued or final message changed", err)
	}
}
