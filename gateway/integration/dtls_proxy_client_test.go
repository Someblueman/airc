//go:build darwin || linux

package gateway_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
)

func dialDTLSProxy(t *testing.T, f dtlsFixture, p *dtlsPacketProxy) *dtls.Conn {
	t.Helper()
	address, err := net.ResolveUDPAddr("udp", p.addr())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.DialWithOptions("udp", address,
		dtls.WithRootCAs(f.cert.Client.RootCAs), dtls.WithServerName("localhost"),
		dtls.WithSupportedProtocols(dtlsALPN), dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256), dtls.WithMTU(1200),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	state, ok := conn.ConnectionState()
	if !ok || state.NegotiatedProtocol != dtlsALPN || len(state.PeerCertificates) == 0 {
		_ = conn.Close()
		t.Fatalf("DTLS proxy identity=%+v", state)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
