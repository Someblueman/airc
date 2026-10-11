//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// The native client sends malformed protobuf bytes, preserving the actual
// transport decoder boundary instead of directly calling an adapter method.
type grpcRawProto struct{}

func (grpcRawProto) Name() string                  { return "proto" }
func (grpcRawProto) Marshal(v any) ([]byte, error) { return v.([]byte), nil }
func (grpcRawProto) Unmarshal(data []byte, v any) error {
	return proto.Unmarshal(data, v.(proto.Message))
}

func TestGRPCRejectsMalformedProtoAndUnknownFields(t *testing.T) {
	f := newGRPCGatewayFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var response v1.Response
	err := f.conn.Invoke(ctx, v1.Gateway_Call_FullMethodName, []byte{0xff}, &response, grpc.ForceCodec(grpcRawProto{}))
	// Without bearer metadata a call is a plain author request: it reaches
	// the protobuf decoder, which refuses the malformed message.
	if status.Code(err) != codes.Internal {
		t.Fatalf("plain malformed protobuf err=%v", err)
	}
	authorized, finish := grpcContext(t, f.token)
	defer finish()
	err = f.conn.Invoke(authorized, v1.Gateway_Call_FullMethodName, []byte{0xff}, &response, grpc.ForceCodec(grpcRawProto{}))
	if status.Code(err) != codes.Internal {
		t.Fatalf("authorized malformed protobuf err=%v", err)
	}
	request := &v1.Request{Envelope: grpcEnvelope(t, core.Request{Version: 1, RequestID: "grpc-unknown", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"})}
	request.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	result, err := f.client.Call(authorized, request)
	if err != nil || result.GetError().GetCode() != string(core.InvalidRequest) {
		t.Fatalf("unknown proto result=%+v err=%v", result, err)
	}
	empty := &v1.Empty{}
	empty.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	result, err = f.client.Capabilities(authorized, empty)
	if err != nil || result.GetError().GetCode() != string(core.InvalidRequest) {
		t.Fatalf("unknown empty result=%+v err=%v", result, err)
	}
	rpcNoJobs(t, f.config.StateDir)
}
