//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/testtls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestGRPCRejectsUntrustedOrPlaintextTLS(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	ctx, cancel := grpcContext(t, f.token)
	defer cancel()
	var remote peer.Peer
	if _, err := f.client.Capabilities(ctx, &v1.Empty{}, grpc.Peer(&remote)); err != nil {
		t.Fatal(err)
	}
	security, ok := remote.AuthInfo.(credentials.TLSInfo)
	if !ok || security.State.Version != tls.VersionTLS13 || security.State.NegotiatedProtocol != "h2" || len(security.State.VerifiedChains) == 0 {
		t.Fatalf("unverified TLS/h2 peer: %+v", remote.AuthInfo)
	}
	oldTLS := f.cert.Client.Clone()
	oldTLS.MinVersion, oldTLS.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	wrongCA := testtls.New(t)
	for _, test := range []struct {
		name  string
		creds credentials.TransportCredentials
	}{
		{"untrusted CA", credentials.NewTLS(wrongCA.Client.Clone())},
		{"TLS 1.2", credentials.NewTLS(oldTLS)},
		{"plaintext", insecure.NewCredentials()},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, err := grpc.NewClient(f.address, grpc.WithTransportCredentials(test.creds))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			callCtx, finish := context.WithTimeout(t.Context(), 1500*time.Millisecond)
			defer finish()
			if _, err = v1.NewGatewayClient(conn).Capabilities(callCtx, &v1.Empty{}); status.Code(err) != codes.Unavailable {
				t.Fatalf("insecure/untrusted transport did not fail handshake: %v", err)
			}
		})
	}
	rpcNoJobs(t, f.config.StateDir)
}
