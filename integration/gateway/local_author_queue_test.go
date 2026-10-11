//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package gateway_test

import (
	"context"
	"crypto/rand"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
)

// A System V queue listener without return routes takes plain author
// requests and answers on its one reply queue (reply_to).
func TestLocalAuthorsOverSystemVQueue(t *testing.T) {
	f := newLocalAuthorFixture(t)
	ingressKey, replyKey := freeSystemVKey(t), freeSystemVKey(t)
	if ingressKey == replyKey {
		t.Fatal("fixture queue key collision")
	}
	reply, err := sysvqueue.Create(replyKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reply.Close() })
	c, _ := f.config(t, config.Listener{ID: "sysv", Protocol: "system_v_queue", Address: strconv.FormatInt(int64(ingressKey), 10), ReplyTo: strconv.FormatInt(int64(replyKey), 10), Operations: []core.Operation{core.Send, core.ReceiptGet}})
	startAuthorGateway(t, c)
	source, err := sysvqueue.Open(ingressKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	exerciseLocalAuthors(t, f, "sysv", func(id string, request map[string]any) plainFrameResponse {
		var datagram [16]byte
		if _, err := rand.Read(datagram[:]); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		for _, fragment := range webTransportDatagrams(plainFrame(t, id, request), datagram) {
			if err := source.WriteMessage(ctx, fragment); err != nil {
				t.Fatal(err)
			}
		}
		return decodePlainResponse(t, id, readAGD1Payload(t, &systemVResponseReader{queue: reply}, time.Now().Add(10*time.Second)))
	})
}
