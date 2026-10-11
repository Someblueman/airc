//go:build darwin || linux

package service

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/quic-go/quic-go"
	wt "github.com/quic-go/webtransport-go"
)

func TestWebTransportOwnerRealHandshakeAndShutdown(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slots := make(chan struct{}, 2)
	owner, err := newWebTransportOwner(ctx, testDispatcher(t), slots, make(chan struct{}, 2), nil, pair, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		ctx, finish := context.WithTimeout(context.Background(), 2*time.Second)
		defer finish()
		if err := owner.close(ctx); err != nil {
			t.Error(err)
		}
	})
	served := make(chan error, 1)
	go func() { served <- owner.serve(ctx) }()
	transport := &wt.Transport{TLSClientConfig: cert.Client.Clone(), ApplicationProtocols: []string{"airc-gateway.v1"}, DialAddr: quic.DialAddr}
	defer transport.Close()
	dialCtx, finish := context.WithTimeout(ctx, 5*time.Second)
	defer finish()
	response, session, err := transport.Dial(dialCtx, "https://"+owner.addr().String()+"/v1/webtransport", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.CloseWithError(0, "")
	state := session.SessionState()
	if response.StatusCode != 200 || state.ApplicationProtocol != "airc-gateway.v1" || state.ConnectionState.TLS.Version != tls.VersionTLS13 || state.ConnectionState.TLS.NegotiatedProtocol != "h3" {
		t.Fatalf("handshake response=%+v state=%+v", response, state)
	}
	// Leave both directions open and silent: shutdown must cancel the server's
	// framed reader, not wait for its normal idle deadline.
	stream, err := session.OpenStreamSync(dialCtx)
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
		t.Fatal("QUIC accept loop not joined")
	}
	if len(slots) != 0 {
		t.Fatal("WebTransport shutdown retained connection admission")
	}
}
