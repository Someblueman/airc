//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisStreamsOwnerRecoveryRefusalCannotPublishOrAck(t *testing.T) {
	for _, mode := range []string{"missing", "unsigned", "deleted", "entry", "principal", "operation", "revoked", "capacity"} {
		t.Run(mode, func(t *testing.T) {
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
			requests := make(chan struct{}, 1)
			switch mode {
			case "missing":
				request = redisStreamRequest(t, broker, request, "2-0")
				entry = redisStreamEntry(t, f, request, -400)
				entry.ID = "2-0"
			case "unsigned":
				entry.Payload = []byte("unsigned")
			case "deleted":
				entry.Deleted = true
				entry.Payload = nil
			case "entry":
				entry.ID = "2-0"
			case "principal":
				broker.Route.Principal = "other"
			case "operation":
				broker.Route.Operations = []core.Operation{core.HistoryRead}
			case "revoked":
				if _, err = f.s.Revoke(t.Context(), f.identity.ID()); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				requests <- struct{}{}
			}
			peerDone := make(chan struct{})
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				defer close(peerDone)
				if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
					return
				}
				_, _ = c.Write([]byte("+OK\r\n"))
				if v, err := rediswire.Read(r); err == nil {
					t.Errorf("refused recovery emitted command: %+v", v)
				}
			})
			broker.Address, broker.TLSServerName, broker.Credential = b.Address, b.TLSServerName, b.Subscriber
			slots := make(chan struct{}, 1)
			p, err := openRedisStream(t.Context(), broker, security, []byte("password"), slots)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			sealer, _, _, _ := redisResultFixture(t)
			complete, err := p.completeStreamEntry(f.c.Realm, f.d, sealer, entry, requests)
			if complete || err != nil {
				t.Fatal("refused entry completed", complete, err)
			}
			p.close()
			<-peerDone
			if len(slots) != 0 || len(requests) != map[bool]int{true: 1, false: 0}[mode == "capacity"] {
				t.Fatal("refusal changed capacity")
			}
			if mode != "revoked" {
				after, err := f.s.Lookup(t.Context(), f.identity, before.Result.RequestID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("refused lookup mutated retained job", err)
				}
			}
		})
	}
}
