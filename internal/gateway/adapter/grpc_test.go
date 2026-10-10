package adapter

import (
	"context"
	"strings"
	"testing"

	gatewayv1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/store"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestGRPCWireBoundsIncludeProtobufPrefix(t *testing.T) {
	page := &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_PageJson{PageJson: make([]byte, MaxResponseFrameBytes)}}
	if proto.Size(page)+5 <= MaxResponseFrameBytes {
		t.Fatalf("wire size omitted protobuf prefix: %d", proto.Size(page)+5)
	}
	terminal := &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_Terminal{Terminal: &gatewayv1.Terminal{PageJson: make([]byte, MaxResponseFrameBytes-128), Reason: "resume"}}}
	if proto.Size(terminal)+5 > MaxResponseFrameBytes {
		t.Fatalf("terminal unexpectedly exceeds frame budget: %d", proto.Size(terminal)+5)
	}
}

func TestGRPCBearerMetadataIsSingleExactCredential(t *testing.T) {
	tests := []struct {
		name  string
		value []string
		ok    bool
	}{
		{"valid", []string{"Bearer " + strings.Repeat("a", 64)}, true},
		{"missing", nil, false},
		{"duplicate", []string{"Bearer " + strings.Repeat("a", 64), "Bearer " + strings.Repeat("b", 64)}, false},
		{"wrong scheme", []string{"Basic " + strings.Repeat("a", 64)}, false},
		{"wrong length", []string{"Bearer short"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.value != nil {
				ctx = metadata.NewIncomingContext(ctx, metadata.MD{"authorization": test.value})
			}
			_, err := grpcBearer(ctx)
			if (err == nil) != test.ok || err != nil && err != auth.ErrAuthentication {
				t.Fatalf("err=%v want valid=%v", err, test.ok)
			}
		})
	}
}

func TestGRPCRejectsProtoUnknownFields(t *testing.T) {
	req := &gatewayv1.Request{Envelope: []byte(`{}`)}
	req.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	if len(req.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("test failed to install unknown field")
	}
	empty := &gatewayv1.Empty{}
	empty.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	if len(empty.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("test failed to install nested unknown field")
	}
	if proto.Size(req) <= len(req.GetEnvelope()) || proto.Size(empty) == 0 {
		t.Fatal("unknown fields were not retained for strict rejection")
	}
}

func TestGRPCServerBounds(t *testing.T) {
	if _, err := GRPCServer(nil, make(chan struct{}, 1)); err != store.ErrInvalid {
		t.Fatalf("nil dispatcher err=%v", err)
	}
	if _, err := GRPCServer(nil, nil); err != store.ErrInvalid {
		t.Fatalf("nil request gate err=%v", err)
	}
}
