//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package gateway_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
)

var _ = registerSuiteCarriers(suiteCarrier{name: "sysvqueue", listener: config.Listener{Protocol: "system_v_queue"}, configure: prepareSystemVSuite, dial: dialSystemVSuite, results: true, reads: true})

// systemVSuiteReplies maps each listener's ingress key to its reply_to
// queue, which the owner provisions and the gateway answers every producer on.
var systemVSuiteReplies sync.Map

func prepareSystemVSuite(t *testing.T, l *config.Listener) {
	t.Helper()
	ingress, replyKey := freeSystemVKey(t), freeSystemVKey(t)
	if ingress == replyKey {
		t.Fatal("queue key collision")
	}
	reply, err := sysvqueue.Create(replyKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reply.Close() })
	l.Address, l.ReplyTo = strconv.FormatInt(int64(ingress), 10), strconv.FormatInt(int64(replyKey), 10)
	systemVSuiteReplies.Store(l.Address, reply)
}

func dialSystemVSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	key, err := strconv.ParseInt(d.address, 10, 32)
	if err != nil {
		return nil, err
	}
	reply, ok := systemVSuiteReplies.Load(d.address)
	if !ok {
		t.Fatalf("no reply queue for %s", d.address)
	}
	source, err := sysvqueue.Open(int32(key))
	if err != nil {
		return nil, err
	}
	return &localSuiteClient{
		write: func(t *testing.T, _ string, frame []byte) {
			t.Helper()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			for _, fragment := range localSuiteDatagrams(t, frame) {
				if err := source.WriteMessage(ctx, fragment); err != nil {
					t.Fatal(err)
				}
			}
		},
		read: func(t *testing.T, _ string, wait time.Duration) []byte {
			t.Helper()
			return readLocalDatagrams(t, &systemVResponseReader{queue: reply.(*sysvqueue.Queue)}, wait)
		},
		done: func() { _ = source.Close() },
	}, nil
}
