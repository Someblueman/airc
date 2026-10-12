package adapter

import (
	"bytes"
	"strings"
	"testing"

	gatewayv1 "github.com/Someblueman/airc/api/gateway/v1"
	"google.golang.org/protobuf/proto"
)

func TestGRPCWireBoundsIncludeProtobufPrefix(t *testing.T) {
	page := &grpcWatchFrame{PageJSON: make([]byte, MaxResponseFrameBytes)}
	if len(page.marshal())+5 <= MaxResponseFrameBytes {
		t.Fatalf("wire size omitted protobuf prefix: %d", len(page.marshal())+5)
	}
	terminal := &grpcWatchFrame{Terminal: &grpcTerminal{PageJSON: make([]byte, MaxResponseFrameBytes-128), Reason: "resume"}}
	if len(terminal.marshal())+5 > MaxResponseFrameBytes {
		t.Fatalf("terminal unexpectedly exceeds frame budget: %d", len(terminal.marshal())+5)
	}
}

// The gateway's encodings are protobuf-go's, byte for byte.
func TestGRPCMessagesMatchProtobufGo(t *testing.T) {
	failures := []*grpcFailureMessage{nil, {}, {Code: "invalid_request"}, {Code: "capacity_exceeded", Transient: true}, {Code: "upstream", UpstreamCode: 433}, {UpstreamCode: -1}}
	generated := func(f *grpcFailureMessage) *gatewayv1.Failure {
		if f == nil {
			return nil
		}
		return &gatewayv1.Failure{Code: f.Code, Transient: f.Transient, UpstreamCode: f.UpstreamCode}
	}
	check := func(name string, ours []byte, theirs proto.Message) {
		t.Helper()
		want, err := proto.MarshalOptions{Deterministic: true}.Marshal(theirs)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ours, want) {
			t.Errorf("%s: got %x, protobuf-go %x", name, ours, want)
		}
	}
	for _, payload := range [][]byte{nil, {}, []byte(`{"ok":true}`), bytes.Repeat([]byte("x"), 300)} {
		check("result", (&grpcResponse{ResultJSON: payload}).marshal(), &gatewayv1.Response{Outcome: &gatewayv1.Response_ResultJson{ResultJson: payload}})
		check("page", (&grpcWatchFrame{PageJSON: payload}).marshal(), &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_PageJson{PageJson: payload}})
		for _, f := range failures {
			if f != nil {
				check("error", (&grpcResponse{Error: f}).marshal(), &gatewayv1.Response{Outcome: &gatewayv1.Response_Error{Error: generated(f)}})
			}
			for _, reason := range []string{"", "resume"} {
				ours := (&grpcWatchFrame{Terminal: &grpcTerminal{PageJSON: payload, Reason: reason, Error: f}}).marshal()
				theirs := &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_Terminal{Terminal: &gatewayv1.Terminal{PageJson: payload, Reason: reason, Error: generated(f)}}}
				check("terminal", ours, theirs)
			}
		}
	}
}

// Decoding agrees with protobuf-go: the same value, the same unknown fields
// and the same malformed inputs.
func FuzzGRPCRequestMatchesProtobufGo(f *testing.F) {
	for _, seed := range [][]byte{
		{}, {0xff}, {0x0a, 0x02, '{', '}'}, {0x0a, 0x00}, {0x10, 0x01},
		{0x0a, 0x01, 'a', 0x0a, 0x01, 'b'}, // the last value wins
		{0x08, 0x01},                       // request_json as a varint is unknown
		{0x1b, 0x08, 0x01, 0x1c},           // a group
		{0x1b, 0x08, 0x01, 0x24},           // a mismatched end group
		{0x0c},                             // a lone end group
		{0x00, 0x01},                       // field number zero
		{0x0a, 0x05, 'a'},                  // truncated bytes
		{0x0f}, {0x11, 1, 2, 3}, {0x15, 1, 2, 3, 4},
		{0x0a, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02},
		{0xf8, 0xff, 0xff, 0xff, 0x0f, 0x01}, // field number 2^29-1
		{0x80, 0x80, 0x80, 0x80, 0x10, 0x01}, // field number 2^29
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var request gatewayv1.Request
		requestErr := proto.Unmarshal(data, &request)
		value, unknown, err := unmarshalGRPCRequest(data, true)
		if (err != nil) != (requestErr != nil) {
			t.Fatalf("Request %x: error %v, protobuf-go %v", data, err, requestErr)
		}
		if err == nil && (!bytes.Equal(value, request.GetRequestJson()) || unknown != (len(request.ProtoReflect().GetUnknown()) != 0)) {
			t.Fatalf("Request %x: %q unknown=%v, protobuf-go %q unknown=%d", data, value, unknown, request.GetRequestJson(), len(request.ProtoReflect().GetUnknown()))
		}
		var empty gatewayv1.Empty
		emptyErr := proto.Unmarshal(data, &empty)
		_, unknown, err = unmarshalGRPCRequest(data, false)
		if (err != nil) != (emptyErr != nil) || err == nil && unknown != (len(empty.ProtoReflect().GetUnknown()) != 0) {
			t.Fatalf("Empty %x: unknown=%v error %v, protobuf-go %v", data, unknown, err, emptyErr)
		}
	})
}

func TestGRPCTimeoutAndMessageEncoding(t *testing.T) {
	for value, ok := range map[string]bool{"1S": true, "100m": true, "99999999H": true, "1": false, "123456789S": false, "5x": false, "-1S": false} {
		if _, err := decodeGRPCTimeout(value); (err == nil) != ok {
			t.Errorf("timeout %q: %v", value, err)
		}
	}
	for in, want := range map[string]string{"plain text": "plain text", "50%": "50%25", "é": "%C3%A9", "a\nb": "a%0Ab", "\xff": "%EF%BF%BD"} {
		if got := encodeGRPCMessage(in); got != want {
			t.Errorf("encode %q = %q, want %q", in, got, want)
		}
	}
	for path, want := range map[string]string{
		"/airc.gateway.v1.Gateway/Nope": "unknown method Nope for service airc.gateway.v1.Gateway",
		"/other.Service/Call":           "unknown service other.Service",
		"nope":                          `malformed method name: "nope"`,
	} {
		if status := grpcUnknownMethod(path); status.code != grpcUnimplemented || !strings.Contains(status.message, want) {
			t.Errorf("%s: %+v", path, status)
		}
	}
}
