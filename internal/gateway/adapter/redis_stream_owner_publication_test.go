//go:build darwin || linux

package adapter

import (
	"bufio"
	"crypto/tls"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisStreamsWholeReadPublicationAndFailureRetention(t *testing.T) {
	for _, mode := range []string{"history", "search", "context", "oversize", "failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			broker := redisStreamBroker(f)
			id, err := config.RedisStreamRequestID(broker.Namespace, broker.Route.Stream, "1-0")
			if err != nil {
				t.Fatal(err)
			}
			result := dispatch.DetachedResult{Version: 1, Operation: core.HistoryRead, Page: &backend.HistoryPage{Cursor: "opaque-cursor", Status: "more", Gap: true}}
			if mode == "search" {
				result.Operation = core.Search
			}
			if mode == "context" {
				result.Operation = core.ContextRead
				result.Page = nil
				result.Context = &core.ContextResult{}
			}
			if mode == "oversize" {
				result.Page.Cursor = strings.Repeat("x", 25000)
			}
			admission := redisAdmission{requestID: id, binding: f.identity.Binding(), result: &result}
			if mode == "failure" {
				admission.result = nil
				admission.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
			}
			completed := mode != "oversize" && mode != "failure"
			results := make(chan []byte, 1)
			peerDone := make(chan struct{})
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				defer close(peerDone)
				if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
					return
				}
				_, _ = c.Write([]byte("+OK\r\n"))
				token := streamPeerResult(t, c, r, broker)
				if token == nil {
					return
				}
				results <- token
				_, _ = c.Write([]byte("$3\r\n2-0\r\n"))
				if completed {
					if !streamPeerCommand(t, r, "XACK", "events", "gateway", "1-0") {
						return
					}
					_, _ = c.Write([]byte(":1\r\n"))
				}
				if v, err := rediswire.Read(r); err == nil {
					t.Errorf("unexpected command after result: %+v", v)
				}
			})
			broker.Address, broker.TLSServerName, broker.Credential = b.Address, b.TLSServerName, b.Subscriber
			slots := make(chan struct{}, 1)
			p, err := openRedisStream(t.Context(), broker, security, []byte("password"), slots)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			sealer, pub, _, _ := redisResultFixture(t)
			got, err := p.publishStreamAdmission(f.c.Realm, sealer, "1-0", admission)
			if err != nil || got != completed {
				t.Fatal("wrong completion decision", got, err)
			}
			p.close()
			<-peerDone
			response := streamOpenResult(t, f, broker, pub, "1-0", <-results)
			if completed {
				if response.Error != nil || response.Result == nil || !reflect.DeepEqual(*response.Result, result) {
					t.Fatal("whole read result changed")
				}
			} else if response.Result != nil || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient {
				t.Fatal("failure result missing")
			}
			if mode == "oversize" && len(result.Page.Cursor) != 25000 {
				t.Fatal("oversize result sliced")
			}
			if len(slots) != 0 {
				t.Fatal("publication leaked slot")
			}
		})
	}
}
