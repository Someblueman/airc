//go:build darwin || linux

package gateway_test

import (
	"context"
	cryptotls "crypto/tls"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/quic-go/quic-go"
)

func TestRawQUICRejectsWrongALPNAndCertificate(t *testing.T) {
	f := newRawQUICFixture(t, false)
	for _, tc := range []struct {
		name  string
		setup func(*cryptotls.Config)
	}{
		{name: "alpn", setup: func(c *cryptotls.Config) { c.NextProtos = []string{"h3"} }},
		{name: "certificate", setup: func(c *cryptotls.Config) { c.ServerName = "wrong.example" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := f.cert.Client.Clone()
			cfg.MinVersion = cryptotls.VersionTLS13
			cfg.NextProtos = []string{rawQUICALPN}
			tc.setup(cfg)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			conn, err := quic.DialAddr(ctx, f.address, cfg, &quic.Config{Allow0RTT: false})
			if err == nil {
				_ = conn.CloseWithError(0, "negative test")
				t.Fatal("raw QUIC invalid TLS/ALPN unexpectedly connected")
			}
			if tc.name == "certificate" {
				var hostname x509.HostnameError
				if !errors.As(err, &hostname) {
					t.Fatalf("wrong-name failure was not certificate validation: %v", err)
				}
			} else {
				var transport *quic.TransportError
				if !errors.As(err, &transport) || transport.ErrorCode != 0x178 {
					t.Fatalf("wrong-ALPN failure was not no_application_protocol: %v", err)
				}
			}
		})
	}
	rpcNoJobs(t, f.config.StateDir)
}

func TestRawQUICPartialFrameClosesStreamWithoutAdmission(t *testing.T) {
	f := newRawQUICFixture(t, false)
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte{0, 0, 1, 0, '{'}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	_ = stream.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	if _, err := stream.Read(make([]byte, 1)); err == nil {
		t.Fatal("raw QUIC partial frame unexpectedly produced a response")
	}
	response := rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-after-partial", Operation: core.CapabilitiesGet}, true)
	if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("raw QUIC connection unusable after partial frame: %+v", response)
	}
	rpcNoJobs(t, f.config.StateDir)
}

func TestRawQUICDatagramListenerRejectsMissingPeerNegotiation(t *testing.T) {
	f := newRawQUICFixture(t, true)
	cfg := f.cert.Client.Clone()
	cfg.NextProtos = []string{rawQUICALPN}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, f.address, cfg, &quic.Config{EnableDatagrams: false})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "test complete")
	select {
	case <-conn.Context().Done():
	case <-ctx.Done():
		t.Fatal("datagram listener retained peer without negotiated extension")
	}
	rpcNoJobs(t, f.config.StateDir)
}
