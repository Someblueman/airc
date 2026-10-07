package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestKafkaReconnectBackoffAndJoin(t *testing.T) {
	attempts := make(chan time.Time, 3)
	rb, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		attempts <- time.Now()
		_ = kafkaPeerAuth(t, c, r, "auth", 0)
		_, _ = r.ReadByte()
	})
	b, _ := kafkaMetadataPlan()
	b.Brokers = []config.KafkaBroker{{ID: 7, Address: rb.Address, TLSServerName: rb.TLSServerName}}
	b.Credential = config.KafkaCredential{Username: "user"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots := make(chan struct{}, 2)
	done := make(chan error, 1)
	go func() {
		done <- runKafkaConnections(ctx, b, map[int32]*tls.Config{7: security}, []byte("password"), slots, func(*kafkaPair) bool { t.Error("failed authentication invoked session"); return false })
	}()
	var previous time.Time
	for i := range 3 {
		select {
		case attempt := <-attempts:
			if i > 0 && attempt.Sub(previous) < kafkaBackoffMin*time.Duration(1<<(i-1)) {
				t.Fatal("failure skipped backoff")
			}
			previous = attempt
		case <-time.After(2 * time.Second):
			t.Fatal("retry missing")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect cancellation did not join")
	}
	if len(slots) != 0 {
		t.Fatal("reconnect retained sockets")
	}
	if kafkaWait(ctx, kafkaBackoffMax) {
		t.Fatal("maximum backoff ignored cancellation")
	}
}

func TestKafkaReconnectCompletedSessions(t *testing.T) {
	ready := make(chan struct{})
	b, _ := kafkaMetadataPlan()
	b.Credential = config.KafkaCredential{Username: "user"}
	rb, security, count := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		<-ready
		if !kafkaPeerAuth(t, c, r, "", 0) {
			return
		}
		_, _, correlation, _, err := kafkaPeerRequest(r)
		if err != nil {
			return
		}
		_ = kafkaPeerReply(c, correlation, kafkaPairMetadataWire(b, 7, 7, 4))
		_, _ = r.ReadByte()
	})
	b.Brokers = []config.KafkaBroker{{ID: 7, Address: rb.Address, TLSServerName: rb.TLSServerName}}
	close(ready)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots := make(chan struct{}, 1)
	calls := 0
	err := runKafkaConnections(ctx, b, map[int32]*tls.Config{7: security}, []byte("password"), slots, func(p *kafkaPair) bool {
		calls++
		if p.request != p.result || len(slots) != 1 {
			t.Error("session got invalid ownership")
		}
		if calls == 2 {
			cancel()
		}
		return true
	})
	if err != nil || calls != 2 || count.Load() != 2 || len(slots) != 0 {
		t.Fatal("session reconnect did not join", err, calls, count.Load())
	}
}
