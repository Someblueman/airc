//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/Someblueman/airc/gateway/api/v1"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// grpcSuiteClient calls Gateway.Call over h2c and streams Gateway.Watch.
type grpcSuiteClient struct {
	conn   *grpc.ClientConn
	client v1.GatewayClient
}

func dialGRPCSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	transport := insecure.NewCredentials()
	if d.tls != nil {
		transport = credentials.NewTLS(d.tls)
	}
	conn, err := grpc.NewClient("passthrough:///"+d.address, grpc.WithTransportCredentials(transport))
	if err != nil {
		return nil, err
	}
	return &grpcSuiteClient{conn: conn, client: v1.NewGatewayClient(conn)}, nil
}

func (c *grpcSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	ctx, cancel := suiteContext(t)
	defer cancel()
	response, err := c.client.Call(ctx, &v1.Request{RequestJson: request})
	if err != nil {
		// Authentication, access and capacity refusals are gRPC statuses.
		code, ok := map[codes.Code]core.ErrorCode{codes.Unauthenticated: core.AuthenticationFailed, codes.PermissionDenied: core.Forbidden, codes.ResourceExhausted: core.CapacityExceeded}[status.Code(err)]
		if !ok {
			t.Fatal(err)
		}
		return suiteReply{failure: &core.Failure{Code: code}}
	}
	if f := response.GetError(); f != nil {
		return suiteReply{failure: &core.Failure{Code: core.ErrorCode(f.GetCode()), Transient: f.GetTransient(), UpstreamCode: int(f.GetUpstreamCode())}}
	}
	var result dispatch.DetachedResult
	if err := json.Unmarshal(response.GetResultJson(), &result); err != nil {
		t.Fatalf("result %s: %v", response.GetResultJson(), err)
	}
	return suiteReply{result: &result}
}

func (c *grpcSuiteClient) send(t *testing.T, request []byte) { c.call(t, request) }

func (c *grpcSuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := c.client.Watch(ctx, &v1.Request{RequestJson: request})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return func() *backend.HistoryPage {
		t.Helper()
		frame, err := stream.Recv()
		if err != nil || frame.GetTerminal() != nil {
			return nil
		}
		var page backend.HistoryPage
		if err := json.Unmarshal(frame.GetPageJson(), &page); err != nil {
			t.Fatalf("watch frame %+v: %v", frame, err)
		}
		return &page
	}, cancel
}

func (c *grpcSuiteClient) close() { _ = c.conn.Close() }
