package service

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestCoAPTCPListenerAdmissionAndJoinedShutdown(t *testing.T) {
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	slots := make(chan struct{}, 1)
	serverTLS := cert.Server.Clone()
	serverTLS.NextProtos = []string{adapter.CoAPALPN}
	secure := tls.NewListener(limitedListener{Listener: listener, slots: slots}, serverTLS)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveCoAPTCPListener(ctx, secure, testDispatcher(t), make(chan struct{}, 1)) }()
	t.Cleanup(func() { cancel(); _ = secure.Close() })
	clientTLS := cert.Client.Clone()
	clientTLS.NextProtos = []string{adapter.CoAPALPN}
	client, err := (&tls.Dialer{Config: clientTLS}).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	state := client.(*tls.Conn).ConnectionState()
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != adapter.CoAPALPN || len(state.VerifiedChains) == 0 {
		t.Fatalf("verified TLS state=%+v", state)
	}
	rejectedCtx, finish := context.WithTimeout(ctx, time.Second)
	defer finish()
	second, err := (&tls.Dialer{Config: clientTLS}).DialContext(rejectedCtx, "tcp", address)
	if err == nil {
		_ = second.Close()
		t.Fatal("second TLS carrier bypassed shared admission")
	}
	if len(slots) != 1 {
		t.Fatalf("active admission slots=%d", len(slots))
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CoAP TLS listener did not join")
	}
	if len(slots) != 0 {
		t.Fatal("joined listener retained admission")
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("joined listener retained TCP socket: %v", err)
	}
	_ = rebound.Close()
}
