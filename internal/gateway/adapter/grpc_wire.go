package adapter

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// This file is the gRPC server wire for api/gateway/v1/gateway.proto: its
// messages in protobuf encoding, and gRPC over HTTP/2 as grpc-go's ServeHTTP
// mode served it. Encodings match protobuf-go's output byte for byte, and
// decoding keeps its rules: the last value of a field wins and a known field
// with another wire type is an unknown field.

// gRPC status codes.
const (
	grpcOK                = 0
	grpcCanceled          = 1
	grpcUnknown           = 2
	grpcInvalidArgument   = 3
	grpcDeadlineExceeded  = 4
	grpcPermissionDenied  = 7
	grpcResourceExhausted = 8
	grpcUnimplemented     = 12
	grpcInternal          = 13
	grpcUnavailable       = 14
	grpcUnauthenticated   = 16
)

// grpcError is a gRPC status.
type grpcError struct {
	code    int
	message string
}

func (e *grpcError) Error() string {
	return fmt.Sprintf("rpc error: code = %d desc = %s", e.code, e.message)
}

func newGRPCError(code int, format string, args ...any) *grpcError {
	return &grpcError{code: code, message: fmt.Sprintf(format, args...)}
}

// grpcStatusOf converts a handler's error as grpc-go did: a status keeps its
// code, a context error maps to Canceled or DeadlineExceeded, anything else
// is Unknown.
func grpcStatusOf(err error) *grpcError {
	var status *grpcError
	switch {
	case err == nil:
		return &grpcError{code: grpcOK}
	case errors.As(err, &status):
		return status
	case errors.Is(err, context.DeadlineExceeded):
		return &grpcError{code: grpcDeadlineExceeded, message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &grpcError{code: grpcCanceled, message: err.Error()}
	}
	return &grpcError{code: grpcUnknown, message: err.Error()}
}

// Messages of airc.gateway.v1.

type grpcFailureMessage struct {
	Code         string
	Transient    bool
	UpstreamCode int32
}

// grpcResponse is Response; exactly one of ResultJSON (when Error is nil)
// and Error is the oneof outcome.
type grpcResponse struct {
	ResultJSON []byte
	Error      *grpcFailureMessage
}

type grpcTerminal struct {
	PageJSON []byte
	Reason   string
	Error    *grpcFailureMessage
}

// grpcWatchFrame is WatchFrame; PageJSON is the event unless Terminal is set.
type grpcWatchFrame struct {
	PageJSON []byte
	Terminal *grpcTerminal
}

func protoAppendTag(b []byte, field, wireType int) []byte {
	return binary.AppendUvarint(b, uint64(field)<<3|uint64(wireType))
}

func protoAppendBytes(b []byte, field int, value []byte) []byte {
	b = protoAppendTag(b, field, 2)
	b = binary.AppendUvarint(b, uint64(len(value)))
	return append(b, value...)
}

func (f *grpcFailureMessage) appendTo(b []byte) []byte {
	if f.Code != "" {
		b = protoAppendBytes(b, 1, []byte(f.Code))
	}
	if f.Transient {
		b = protoAppendTag(b, 2, 0)
		b = append(b, 1)
	}
	if f.UpstreamCode != 0 {
		b = protoAppendTag(b, 3, 0)
		b = binary.AppendUvarint(b, uint64(int64(f.UpstreamCode)))
	}
	return b
}

func (r *grpcResponse) marshal() []byte {
	if r.Error != nil {
		return protoAppendBytes(nil, 2, r.Error.appendTo(nil))
	}
	return protoAppendBytes(nil, 1, r.ResultJSON)
}

func (t *grpcTerminal) appendTo(b []byte) []byte {
	if len(t.PageJSON) != 0 {
		b = protoAppendBytes(b, 1, t.PageJSON)
	}
	if t.Reason != "" {
		b = protoAppendBytes(b, 2, []byte(t.Reason))
	}
	if t.Error != nil {
		b = protoAppendBytes(b, 3, t.Error.appendTo(nil))
	}
	return b
}

func (f *grpcWatchFrame) marshal() []byte {
	if f.Terminal != nil {
		return protoAppendBytes(nil, 2, f.Terminal.appendTo(nil))
	}
	return protoAppendBytes(nil, 1, f.PageJSON)
}

var errProtoInvalid = errors.New("proto: cannot parse invalid wire-format data")

// unmarshalGRPCRequest decodes Request{bytes request_json = 1}, or Empty
// when requestField is false. unknown reports fields Request does not know,
// including request_json with another wire type.
func unmarshalGRPCRequest(data []byte, requestField bool) (requestJSON []byte, unknown bool, err error) {
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 || tag>>3 > 1<<29-1 {
			return nil, false, errProtoInvalid
		}
		data = data[n:]
		field, wireType := tag>>3, int(tag&7)
		if requestField && field == 1 && wireType == 2 {
			value, rest, ok := protoConsumeBytes(data)
			if !ok {
				return nil, false, errProtoInvalid
			}
			requestJSON, data = value, rest
			continue
		}
		rest, ok := protoConsumeValue(data, field, wireType, 0)
		if !ok {
			return nil, false, errProtoInvalid
		}
		data, unknown = rest, true
	}
	return requestJSON, unknown, nil
}

func protoConsumeBytes(data []byte) (value, rest []byte, ok bool) {
	length, n := binary.Uvarint(data)
	if n <= 0 || length > uint64(len(data)-n) {
		return nil, nil, false
	}
	return data[n : n+int(length)], data[n+int(length):], true
}

// protoConsumeValue skips one field value, including a whole group.
func protoConsumeValue(data []byte, field uint64, wireType, depth int) ([]byte, bool) {
	switch wireType {
	case 0:
		_, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, false
		}
		return data[n:], true
	case 1:
		if len(data) < 8 {
			return nil, false
		}
		return data[8:], true
	case 2:
		_, rest, ok := protoConsumeBytes(data)
		return rest, ok
	case 3:
		if depth >= 10000 {
			return nil, false
		}
		for {
			tag, n := binary.Uvarint(data)
			if n <= 0 || tag>>3 == 0 || tag>>3 > 1<<29-1 {
				return nil, false
			}
			data = data[n:]
			if int(tag&7) == 4 {
				return data, tag>>3 == field
			}
			var ok bool
			if data, ok = protoConsumeValue(data, tag>>3, int(tag&7), depth+1); !ok {
				return nil, false
			}
		}
	case 5:
		if len(data) < 4 {
			return nil, false
		}
		return data[4:], true
	}
	return nil, false
}

// grpcStream is one gRPC call on an HTTP/2 request, written the way grpc-go's
// ServeHTTP transport wrote it: headers with the request's content type and
// declared trailers, then length-prefixed messages, then the status in
// trailers.
type grpcStream struct {
	w           http.ResponseWriter
	contentType string
	headers     bool
}

func (s *grpcStream) writeHeaders() {
	if s.headers {
		return
	}
	s.headers = true
	h := s.w.Header()
	h["Date"] = nil
	h.Set("Content-Type", s.contentType)
	h.Add("Trailer", "Grpc-Status")
	h.Add("Trailer", "Grpc-Message")
	h.Add("Trailer", "Grpc-Status-Details-Bin")
}

func (s *grpcStream) send(message []byte) error {
	if len(message) > grpcMaxSend {
		return newGRPCError(grpcResourceExhausted, "grpc: trying to send message larger than max (%d vs. %d)", len(message), grpcMaxSend)
	}
	s.writeHeaders()
	frame := make([]byte, 5, 5+len(message))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(message)))
	if _, err := s.w.Write(append(frame, message...)); err != nil {
		return err
	}
	return http.NewResponseController(s.w).Flush()
}

func (s *grpcStream) finish(status *grpcError) {
	s.writeHeaders()
	_ = http.NewResponseController(s.w).Flush()
	h := s.w.Header()
	h.Set("Grpc-Status", strconv.Itoa(status.code))
	if status.message != "" {
		h.Set("Grpc-Message", encodeGRPCMessage(status.message))
	}
}

// grpcReadMessage reads one length-prefixed message. io.EOF means the client
// sent no further message.
func grpcReadMessage(body io.Reader, recvCompress string) ([]byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(body, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if int64(length) > grpcMaxRecv {
		return nil, newGRPCError(grpcResourceExhausted, "grpc: received message larger than max (%d vs. %d)", length, grpcMaxRecv)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(body, data); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	switch header[0] {
	case 0:
	case 1:
		if recvCompress == "" || recvCompress == "identity" {
			return nil, newGRPCError(grpcInternal, "grpc: compressed flag set with identity or empty encoding")
		}
		return nil, newGRPCError(grpcUnimplemented, "grpc: Decompressor is not installed for grpc-encoding %q", recvCompress)
	default:
		return nil, newGRPCError(grpcInternal, "grpc: received unexpected payload format %d", header[0])
	}
	return data, nil
}

// grpcContentType reports whether a request's content type is gRPC:
// application/grpc, optionally followed by + or ; and a subtype.
func grpcContentType(contentType string) bool {
	rest, ok := strings.CutPrefix(contentType, "application/grpc")
	return ok && (rest == "" || rest[0] == '+' || rest[0] == ';')
}

// decodeGRPCTimeout parses a grpc-timeout header: up to eight digits and a
// unit of H, M, S, m, u or n.
func decodeGRPCTimeout(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("transport: timeout string is too short: %q", s)
	}
	if len(s) > 9 {
		return 0, fmt.Errorf("transport: timeout string is too long: %q", s)
	}
	unit, ok := map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second, 'm': time.Millisecond, 'u': time.Microsecond, 'n': time.Nanosecond}[s[len(s)-1]]
	if !ok {
		return 0, fmt.Errorf("transport: timeout unit is not recognized: %q", s)
	}
	t, err := strconv.ParseUint(s[:len(s)-1], 10, 64)
	if err != nil {
		return 0, err
	}
	if unit == time.Hour && t > math.MaxInt64/uint64(time.Hour) {
		return time.Duration(math.MaxInt64), nil
	}
	return unit * time.Duration(t), nil
}

// encodeGRPCMessage percent-encodes a grpc-message value: bytes outside
// printable ASCII, and %, become %XX; invalid UTF-8 becomes U+FFFD first.
func encodeGRPCMessage(message string) string {
	plain := true
	for i := 0; i < len(message); i++ {
		if c := message[i]; c < ' ' || c > '~' || c == '%' {
			plain = false
			break
		}
	}
	if plain {
		return message
	}
	var b strings.Builder
	for len(message) > 0 {
		r, size := utf8.DecodeRuneInString(message)
		for _, c := range []byte(string(r)) {
			if size == 1 && c >= ' ' && c <= '~' && c != '%' {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		message = message[size:]
	}
	return b.String()
}
