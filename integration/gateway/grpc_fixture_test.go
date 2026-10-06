//go:build darwin || linux

package gateway_test

import (
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

type grpcGatewayFixture struct {
	process    *gatewayProcess
	address    string
	token      string
	otherToken string
	config     config.Config
	cert       testtls.Certificate
	observer   *irc.Client
	conn       *grpc.ClientConn
	client     v1.GatewayClient
}

func newGRPCGatewayFixture(t *testing.T) grpcGatewayFixture {
	t.Helper()
	return newGRPCGatewayFixtureWithLimits(t, config.Defaults().Limits)
}

func newGRPCGatewayFixtureWithLimits(t *testing.T, limits config.Limits) grpcGatewayFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	observer := dial(t, native, "grpc-fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "grpc-fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	c.Limits = limits
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	c.Principals[0].Operations = ops
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	other := c.Principals[0]
	other.ID = "other"
	other.Operations = []core.Operation{core.ReceiptGet}
	other.Targets = []core.Target{{Kind: "room", Name: "#other-private"}}
	other.BearerTokenFile = filepath.Join(filepath.Dir(other.BearerTokenFile), "other.token")
	if err := auth.CreateCredential(other.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, other)
	cert := testtls.New(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "grpc", Protocol: "grpc", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops}}
	p := startGateway(t, gatewayBinary(t), c)
	t.Cleanup(func() { _ = observer.Close() })
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := auth.ReadToken(other.BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	address := mcpGatewayAddress(t, p)
	tlsConfig := cert.Client.Clone()
	tlsConfig.MinVersion = cryptotls.VersionTLS13
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return grpcGatewayFixture{process: p, address: address, token: token, otherToken: otherToken, config: c, cert: cert, observer: observer, conn: conn, client: v1.NewGatewayClient(conn)}
}

func grpcContext(t *testing.T, token string) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), cancel
}

func grpcEnvelope(t *testing.T, request core.Request) []byte {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
