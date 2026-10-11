package adapter

import (
	"testing"

	gatewayv1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/store"
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

func TestGRPCRejectsProtoUnknownFields(t *testing.T) {
	req := &gatewayv1.Request{RequestJson: []byte(`{}`)}
	req.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	if len(req.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("test failed to install unknown field")
	}
	empty := &gatewayv1.Empty{}
	empty.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	if len(empty.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("test failed to install nested unknown field")
	}
	if proto.Size(req) <= len(req.GetRequestJson()) || proto.Size(empty) == 0 {
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
