//go:build darwin || linux

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/quic-go/quic-go"
)

func TestHTTP3RealHandshakeUsesSharedConnectionAdmission(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	owner, err := newHTTP3Owner(t.Context(), testDispatcher(t), slots, cap(slots), nil, pair, "127.0.0.1:0", adapter.Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := owner.close(ctx); err != nil {
			t.Error(err)
		}
	})
	cfg := cert.Client.Clone()
	cfg.NextProtos = []string{"h3"}
	// An occupied TCP/shared slot must prevent a QUIC handshake, before any
	// application handler or native session can start.
	slots <- struct{}{}
	blocked, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	conn, err := quic.DialAddr(blocked, owner.addr().String(), cfg, nil)
	cancel()
	if err == nil {
		_ = conn.CloseWithError(0, "")
		t.Fatal("QUIC handshake bypassed full shared connection capacity")
	}
	<-slots
	ctx, finish := context.WithTimeout(t.Context(), 2*time.Second)
	defer finish()
	conn, err = quic.DialAddr(ctx, owner.addr().String(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatal("completed real handshake has no admission slot")
	}
	_ = conn.CloseWithError(0, "")
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(slots) != 0 {
		select {
		case <-deadline.C:
			t.Fatal("closed real QUIC connection retained slot")
		case <-time.After(time.Millisecond):
		}
	}
}
