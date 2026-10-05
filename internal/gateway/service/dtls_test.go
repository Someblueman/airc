package service

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/pion/dtls/v3"
)

func TestDTLSOwnerActualHandshakeAdmissionAndJoinedShutdown(t *testing.T) {
	cert := testtls.NewP256(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	owner, err := newDTLSOwner(&dispatch.Dispatcher{}, slots, make(chan struct{}, 2), pair, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	t.Cleanup(func() {
		cancel()
		if err := owner.close(); err != nil {
			t.Error(err)
		}
	})
	served := make(chan error, 1)
	go func() { served <- owner.serve(ctx) }()
	address := owner.addr().String()
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	options := []dtls.ClientOption{
		dtls.WithRootCAs(cert.Client.RootCAs), dtls.WithServerName("localhost"),
		dtls.WithSupportedProtocols(adapter.DTLSALPN), dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256), dtls.WithMTU(1200),
	}
	conn, err := dtls.DialWithOptions("udp", remote, options...)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	handshakeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	if err := conn.HandshakeContext(handshakeCtx); err != nil {
		t.Fatal(err)
	}
	state, ok := conn.ConnectionState()
	if !ok || state.NegotiatedProtocol != adapter.DTLSALPN || state.CipherSuiteID != dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 || len(state.PeerCertificates) == 0 {
		t.Fatalf("DTLS authenticated handshake=%+v ok=%t", state, ok)
	}
	second, err := dtls.DialWithOptions("udp", remote, options...)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	rejectedCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	defer stop()
	if err := second.HandshakeContext(rejectedCtx); err == nil {
		t.Fatal("second handshake bypassed shared admission")
	}
	if len(slots) != 1 {
		t.Fatalf("admission slots=%d", len(slots))
	}
	cancel()
	closed := make(chan error, 1)
	go func() { closed <- owner.close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DTLS owner did not join")
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatal("DTLS retained admission after joined shutdown")
	}
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("DTLS retained UDP socket: %v", err)
	}
	_ = packet.Close()
}

func TestDTLSOwnerCloseBeforeServingReleasesUDP(t *testing.T) {
	cert := testtls.NewP256(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newDTLSOwner(&dispatch.Dispatcher{}, make(chan struct{}, 1), make(chan struct{}, 1), pair, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := owner.addr().String()
	for range 2 {
		if err := owner.close(); err != nil {
			t.Fatal(err)
		}
	}
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("unserved DTLS retained UDP socket: %v", err)
	}
	_ = packet.Close()
}
