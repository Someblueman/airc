//go:build darwin || linux

package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/quic-go/quic-go"
)

func TestRawQUICOwnerHandshakeAdmissionAndJoinedShutdown(t *testing.T) {
	for _, datagrams := range []bool{false, true} {
		t.Run(map[bool]string{false: "streams", true: "datagrams"}[datagrams], func(t *testing.T) {
			cert := testtls.New(t)
			pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			slots := make(chan struct{}, 1)
			owner, err := newRawQUICOwner(ctx, &dispatch.Dispatcher{}, slots, make(chan struct{}, 2), pair, "127.0.0.1:0", datagrams)
			if err != nil {
				t.Fatal(err)
			}
			address := owner.addr().String()
			t.Cleanup(func() {
				cancel()
				shutdown, finish := context.WithTimeout(context.Background(), 2*time.Second)
				defer finish()
				if err := owner.close(shutdown); err != nil {
					t.Error(err)
				}
			})
			served := make(chan error, 1)
			go func() { served <- owner.serve(ctx) }()
			clientTLS := cert.Client.Clone()
			clientTLS.NextProtos = []string{adapter.QUICALPN}
			dialCtx, finish := context.WithTimeout(ctx, 3*time.Second)
			defer finish()
			conn, err := quic.DialAddr(dialCtx, address, clientTLS, &quic.Config{EnableDatagrams: datagrams})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseWithError(0, "")
			state := conn.ConnectionState()
			if !state.TLS.HandshakeComplete || state.TLS.Version != tls.VersionTLS13 || state.TLS.NegotiatedProtocol != adapter.QUICALPN || state.SupportsDatagrams.Remote != datagrams {
				t.Fatalf("handshake=%+v", state)
			}
			// A second real handshake must fail at the shared admission boundary.
			second, err := quic.DialAddr(dialCtx, address, clientTLS, &quic.Config{EnableDatagrams: datagrams})
			if second != nil {
				_ = second.CloseWithError(0, "")
				t.Fatal("full admission accepted second handshake")
			}
			var refusal *quic.TransportError
			if !errors.As(err, &refusal) || refusal.ErrorCode != quic.ConnectionRefused {
				t.Fatalf("admission error=%v", err)
			}
			stream, err := conn.OpenStreamSync(dialCtx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Write([]byte{0}); err != nil {
				t.Fatal(err)
			}
			cancel()
			shutdown, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			if err := owner.close(shutdown); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-served:
				if err != nil {
					t.Fatal(err)
				}
			case <-shutdown.Done():
				t.Fatal("QUIC owner did not join")
			}
			if len(slots) != 0 {
				t.Fatal("QUIC retained admission after shutdown")
			}
			packet, err := net.ListenPacket("udp", address)
			if err != nil {
				t.Fatalf("QUIC retained UDP socket: %v", err)
			}
			_ = packet.Close()
		})
	}
}

func TestRawQUICOwnerCloseBeforeServingReleasesUDP(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newRawQUICOwner(t.Context(), &dispatch.Dispatcher{}, make(chan struct{}, 1), make(chan struct{}, 1), pair, "127.0.0.1:0", false)
	if err != nil {
		t.Fatal(err)
	}
	address := owner.addr().String()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range 2 {
		if err := owner.close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("unserved owner retained UDP socket: %v", err)
	}
	_ = packet.Close()
}
