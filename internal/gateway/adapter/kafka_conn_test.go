package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"math"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestKafkaConnAuthenticatedCommand(t *testing.T) {
	done := make(chan struct{})
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		defer close(done)
		if !kafkaPeerAuth(t, c, r, "", 0) {
			return
		}
		api, version, correlation, body, err := kafkaPeerRequest(r)
		if err != nil || api != 2 || version != 5 || correlation != 3 || string(body) != "request" {
			t.Error("incorrect post-authentication command")
			return
		}
		_ = kafkaPeerReply(c, correlation, []byte("reply"))
		_, _ = r.ReadByte()
	})
	slots := make(chan struct{}, 1)
	p, err := openKafka(t.Context(), kafkaFixtureBroker(b), security, config.KafkaCredential{Username: "user"}, []byte("password"), slots)
	if err != nil || p == nil {
		t.Fatal("verified setup refused", err)
	}
	defer p.close()
	reply, err := p.command(2, 5, []byte("request"), time.Time{})
	if err != nil || string(reply) != "reply" {
		t.Fatal("command failed", err)
	}
	p.correlation = math.MaxInt32
	if _, err := p.command(18, 0, nil, time.Time{}); err != errKafkaConnection {
		t.Fatal("correlation exhaustion reused connection")
	}
	p.close()
	p.close()
	if len(slots) != 0 {
		t.Fatal("capacity leaked")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("peer socket remained open")
	}
}

func TestKafkaConnSetupRefusal(t *testing.T) {
	for _, mode := range []string{"versions", "handshake", "auth", "correlation", "lifetime"} {
		t.Run(mode, func(t *testing.T) {
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				lifetime := int64(0)
				if mode == "lifetime" {
					lifetime = math.MaxInt64
				}
				_ = kafkaPeerAuth(t, c, r, mode, lifetime)
			})
			slots := make(chan struct{}, 1)
			p, err := openKafka(t.Context(), kafkaFixtureBroker(b), security, config.KafkaCredential{Username: "user"}, []byte("password"), slots)
			if err != errKafkaConnection || p != nil || len(slots) != 0 {
				t.Fatal("failed setup retained owner/capacity")
			}
		})
	}
}

func TestKafkaConnCancelAndExpiry(t *testing.T) {
	for _, mode := range []string{"cancel", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			done := make(chan struct{})
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				defer close(done)
				lifetime := int64(0)
				if mode == "expiry" {
					lifetime = 100
				}
				if kafkaPeerAuth(t, c, r, "", lifetime) {
					_, _ = r.ReadByte()
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			slots := make(chan struct{}, 1)
			p, err := openKafka(ctx, kafkaFixtureBroker(b), security, config.KafkaCredential{Username: "user"}, []byte("password"), slots)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			if mode == "cancel" {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancel/expiry did not close raw socket")
			}
			p.close()
			if len(slots) != 0 {
				t.Fatal("capacity released before joined close")
			}
			if _, err := p.command(18, 0, nil, time.Time{}); err != errKafkaConnection {
				t.Fatal("closed owner reused")
			}
		})
	}
}
