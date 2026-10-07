package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
)

func TestRedisStreamConnOpenFailure(t *testing.T) {
	for _, mode := range []string{"certificate", "capacity", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			b, security, count := redisFixture(t, func(_ int, c *tls.Conn, _ *bufio.Reader) { _ = c.Handshake() })
			broker := streamConnBroker(b)
			slots := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "certificate":
				broker.TLSServerName = "wrong.example"
				security = security.Clone()
				security.ServerName = broker.TLSServerName
			case "capacity":
				slots <- struct{}{}
			case "canceled":
				cancel()
			}
			expected := len(slots)
			p, err := openRedisStream(ctx, broker, security, []byte("password"), slots)
			if err != errRedisConnection || p != nil || len(slots) != expected {
				t.Fatal("failed setup leaked or authenticated", err)
			}
			if mode == "capacity" && count.Load() != 0 {
				t.Fatal("dialed without capacity")
			}
		})
	}
}
