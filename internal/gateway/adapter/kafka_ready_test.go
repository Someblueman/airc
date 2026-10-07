package adapter

import (
	"context"
	"testing"
	"time"
)

func TestKafkaReadyChecksExpiryWithoutTimerCallback(t *testing.T) {
	p := &kafkaConn{ctx: t.Context()}
	if !p.ready() {
		t.Fatal("unexpired session refused")
	}
	p.expiry = time.Now().Add(time.Second)
	if !p.ready() {
		t.Fatal("live lifetime refused")
	}
	// No cancellation timer is installed: delayed callbacks cannot grant readiness.
	p.expiry = time.Now().Add(-time.Second)
	if p.ready() {
		t.Fatal("expired session ready before cancellation callback")
	}
	ctx, cancel := context.WithCancel(t.Context())
	p.ctx = ctx
	p.expiry = time.Time{}
	cancel()
	if p.ready() {
		t.Fatal("canceled session ready")
	}
	if (*kafkaConn)(nil).ready() {
		t.Fatal("nil session ready")
	}
}
