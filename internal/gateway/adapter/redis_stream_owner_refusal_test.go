//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// Refused entries publish nothing and stay pending; the owner neither
// correlates a result nor acknowledges them.
func TestRedisStreamsOwnerRefusalCannotPublishOrAck(t *testing.T) {
	for _, mode := range []string{"malformed", "deleted", "target", "principal", "operation"} {
		t.Run(mode, func(t *testing.T) {
			f := newRedisFixture(t)
			broker := redisStreamBroker()
			request := redisRequest()
			entry := redisStreamEntry(t, request)
			requests := make(chan struct{}, 1)
			switch mode {
			case "malformed":
				entry.Payload = []byte("not a contract request")
			case "deleted":
				entry.Deleted = true
				entry.Payload = nil
			case "target":
				broker.Route.Target.Name = "#other"
			case "principal":
				broker.Route.Principal = "other"
			case "operation":
				broker.Route.Operations = []core.Operation{core.HistoryRead}
			}
			peerDone := make(chan struct{})
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				defer close(peerDone)
				if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
					return
				}
				_, _ = c.Write([]byte("+OK\r\n"))
				if v, err := rediswire.Read(r); err == nil {
					t.Errorf("refused entry emitted command: %+v", v)
				}
			})
			broker.Address, broker.TLSServerName, broker.Credential = b.Address, b.TLSServerName, b.Subscriber
			slots := make(chan struct{}, 1)
			p, err := openRedisStream(t.Context(), broker, security, []byte("password"), slots)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			complete, err := p.completeStreamEntry(f.d, entry, requests)
			if complete || err != nil {
				t.Fatal("refused entry completed", complete, err)
			}
			p.close()
			<-peerDone
			if len(slots) != 0 || len(requests) != 0 {
				t.Fatal("refusal changed capacity")
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refused entry admitted", err)
			}
		})
	}
}
