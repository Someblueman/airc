//go:build darwin || linux

package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

func TestGRPCDurableRevocationPrecedesProtobufAndCreatesNoJobs(t *testing.T) {
	native := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	c, identity := acceptanceJournalFixture(t, native, account)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.Watch)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	nativeBackend, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := dispatch.New(journal, verifier, nativeBackend, cursors, c.Principals[0].Operations)
	if err != nil {
		t.Fatal(err)
	}
	server, err := adapter.GRPCServer(dispatcher, make(chan struct{}, 8))
	if err != nil {
		t.Fatal(err)
	}
	tlsServer := httptest.NewUnstartedServer(server.Handler)
	tlsServer.Config = server
	tlsServer.TLS = server.TLSConfig.Clone()
	tlsServer.EnableHTTP2 = true
	tlsServer.StartTLS()
	defer tlsServer.Close()
	clientTLS := tlsServer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conn, err := grpc.NewClient(strings.TrimPrefix(tlsServer.URL, "https://"), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := grpcContext(t, token)
	defer cancel()
	client := v1.NewGatewayClient(conn)
	if _, err = client.Capabilities(ctx, &v1.Empty{}); err != nil {
		t.Fatal(err)
	}
	baseline := nativeObserverCount(t, native)
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := client.Watch(watchCtx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-revoke-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitNativeObserverCount(t, native, baseline+1)
	if _, err = journal.Revoke(t.Context(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	stop()
	waitNativeObserverCount(t, native, baseline)
	var response v1.Response
	err = conn.Invoke(ctx, v1.Gateway_Call_FullMethodName, []byte{0xff}, &response, grpc.ForceCodec(grpcRawProto{}))
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revocation did not precede malformed proto: %v", err)
	}
	_, err = client.Call(ctx, &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-revoke-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"})})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revoked send err=%v", err)
	}
	rpcNoJobs(t, c.StateDir)
}
