package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestKafkaConnBeforeAuthenticationRefusal(t *testing.T) {
	for _, mode := range []string{"certificate", "capacity", "canceled", "insecure", "dns", "password"} {
		t.Run(mode, func(t *testing.T) {
			b, security, count := redisFixture(t, func(_ int, c *tls.Conn, _ *bufio.Reader) { _ = c.Handshake() })
			broker := kafkaFixtureBroker(b)
			slots := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			password := []byte("password")
			switch mode {
			case "certificate":
				broker.TLSServerName = "wrong.example"
				security = security.Clone()
				security.ServerName = broker.TLSServerName
			case "capacity":
				slots <- struct{}{}
			case "canceled":
				cancel()
			case "insecure":
				security = security.Clone()
				security.InsecureSkipVerify = true
			case "dns":
				broker.Address = "localhost:9093"
			case "password":
				password = []byte{'x', 0}
			}
			expected := len(slots)
			p, err := openKafka(ctx, broker, security, config.KafkaCredential{Username: "user"}, password, slots)
			if err != errKafkaConnection || p != nil || len(slots) != expected {
				t.Fatal("invalid setup acquired owner or leaked capacity")
			}
			if mode != "certificate" && count.Load() != 0 {
				t.Fatal("dialed refused input")
			}
		})
	}
}

func TestKafkaConnCancelBlockedSetup(t *testing.T) {
	ready, closed := make(chan struct{}), make(chan struct{})
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		defer close(closed)
		if _, _, _, _, err := kafkaPeerRequest(r); err != nil {
			return
		}
		close(ready)
		_, _ = r.ReadByte()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		p, e := openKafka(ctx, kafkaFixtureBroker(b), security, config.KafkaCredential{Username: "user"}, []byte("password"), slots)
		if p != nil {
			p.close()
		}
		result <- e
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("setup did not reach peer")
	}
	cancel()
	select {
	case e := <-result:
		if e != errKafkaConnection {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked setup did not join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("raw peer remained open")
	}
	if len(slots) != 0 {
		t.Fatal("blocked setup retained capacity")
	}
}

func TestKafkaConnSetupDeadline(t *testing.T) {
	closed := make(chan struct{})
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		defer close(closed)
		if _, _, _, _, err := kafkaPeerRequest(r); err != nil {
			return
		}
		_, _ = r.ReadByte()
	})
	slots := make(chan struct{}, 1)
	start := time.Now()
	p, err := openKafka(t.Context(), kafkaFixtureBroker(b), security, config.KafkaCredential{Username: "user"}, []byte("password"), slots)
	if err != errKafkaConnection || p != nil || len(slots) != 0 {
		t.Fatal("timed-out setup retained owner")
	}
	if time.Since(start) > 6*time.Second {
		t.Fatal("setup deadline restarted or ignored")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("setup timeout left raw socket open")
	}
}
