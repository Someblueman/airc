package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const (
	grpcMaxRecv    = core.MaxRequestBytes + 1024
	grpcMaxSend    = 1 << 20
	grpcWatchLease = 20 * time.Second
)

type grpcHTTPWriterKey struct{}

// grpcAuthorKey carries the ingress of a call: its request_json is a plain
// contract request run as its author.
type grpcAuthorKey struct{}

func grpcAuthor(ctx context.Context) (dispatch.Ingress, bool) {
	in, ok := ctx.Value(grpcAuthorKey{}).(dispatch.Ingress)
	return in, ok
}

func grpcSetWriteDeadline(ctx context.Context, timeout time.Duration) error {
	if controller, ok := ctx.Value(grpcHTTPWriterKey{}).(*http.ResponseController); ok {
		return controller.SetWriteDeadline(time.Now().Add(timeout))
	}
	return http.ErrNotSupported
}

func grpcReply(ctx context.Context, response *grpcResponse) (*grpcResponse, error) {
	if err := grpcSetWriteDeadline(ctx, 3*time.Second); err != nil {
		return nil, newGRPCError(grpcUnavailable, "response unavailable")
	}
	if len(response.marshal())+5 > grpcMaxSend {
		response = grpcFailureResponse(store.ErrCapacity)
	}
	return response, nil
}

// GRPCServer returns an HTTP/2-only server for the Gateway service of
// api/gateway/v1/gateway.proto. The service owns listener, optional TLS and
// shutdown lifecycle. Every call is a plain author request and, as on IRC,
// needs no TLS (cleartext HTTP/2 prior knowledge, h2c).
func GRPCServer(d *dispatch.Dispatcher, requests chan struct{}) (*http.Server, error) {
	if d == nil || !d.AuthorAccess() || requests == nil || cap(requests) < 1 || cap(requests) > 256 {
		return nil, store.ErrInvalid
	}
	g := &grpcGateway{d: d}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.URL.RawQuery != "" || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("X-Forwarded-Host")) != 0 || len(r.Header.Values("X-Original-Host")) != 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), grpcWatchLease)
		defer cancel()
		ctx = context.WithValue(ctx, grpcHTTPWriterKey{}, http.NewResponseController(w))
		defer func() { _ = grpcSetWriteDeadline(ctx, 5*time.Second) }()
		if err := grpcSetWriteDeadline(ctx, 23*time.Second); err != nil {
			writeGRPCHTTPStatus(w, newGRPCError(grpcUnavailable, "response unavailable"))
			return
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		default:
			writeGRPCHTTPStatus(w, newGRPCError(grpcResourceExhausted, "capacity exceeded"))
			return
		}
		ctx = context.WithValue(ctx, grpcAuthorKey{}, dispatch.Ingress{Source: d.RemoteSource(r.RemoteAddr)})
		g.serveHTTP(w, r.WithContext(ctx))
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Handler:           h,
		Protocols:         protocols,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}},
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
		HTTP2:             &http.HTTP2Config{MaxConcurrentStreams: cap(requests)},
	}, nil
}

type grpcGateway struct {
	d *dispatch.Dispatcher
}

// The Gateway service's methods, as gRPC paths.
const (
	grpcService            = "airc.gateway.v1.Gateway"
	grpcCallMethod         = "/" + grpcService + "/Call"
	grpcCapabilitiesMethod = "/" + grpcService + "/Capabilities"
	grpcWatchMethod        = "/" + grpcService + "/Watch"
)

func writeGRPCHTTPStatus(w http.ResponseWriter, status *grpcError) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	w.Header().Set("Grpc-Status", strconv.Itoa(status.code))
	w.Header().Set("Grpc-Message", status.message)
	w.WriteHeader(http.StatusOK)
}

// serveHTTP serves one gRPC call with grpc-go's ServeHTTP checks and errors.
func (g *grpcGateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, fmt.Sprintf("invalid gRPC request method %q", r.Method), http.StatusMethodNotAllowed)
		return
	}
	contentType := r.Header.Get("Content-Type")
	if !grpcContentType(contentType) {
		http.Error(w, fmt.Sprintf("invalid gRPC request content-type %q", contentType), http.StatusUnsupportedMediaType)
		return
	}
	if r.ProtoMajor != 2 {
		http.Error(w, "gRPC requires HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	if value := r.Header.Get("Grpc-Timeout"); value != "" {
		timeout, err := decodeGRPCTimeout(value)
		if err != nil {
			cancel()
			http.Error(w, fmt.Sprintf("malformed grpc-timeout: %v", err), http.StatusBadRequest)
			return
		}
		ctx, cancel = context.WithTimeout(r.Context(), timeout)
	}
	defer cancel()
	stream := &grpcStream{w: w, contentType: contentType}
	defer r.Body.Close()
	method := r.URL.Path
	if method != grpcCallMethod && method != grpcCapabilitiesMethod && method != grpcWatchMethod {
		stream.finish(grpcUnknownMethod(method))
		return
	}
	if encoding := r.Header.Get("Grpc-Encoding"); encoding != "" && encoding != "identity" {
		stream.finish(newGRPCError(grpcUnimplemented, "grpc: Decompressor is not installed for grpc-encoding %q", encoding))
		return
	}
	data, err := grpcReadMessage(r.Body, r.Header.Get("Grpc-Encoding"))
	if method == grpcWatchMethod {
		// A server-streaming request is exactly one message, then end of stream.
		switch {
		case errors.Is(err, io.EOF):
			err = newGRPCError(grpcInternal, "cardinality violation: received no request message from non-client-streaming RPC")
		case errors.Is(err, io.ErrUnexpectedEOF):
			err = newGRPCError(grpcInternal, "%s", err.Error())
		case err == nil:
			if _, err = grpcReadMessage(r.Body, r.Header.Get("Grpc-Encoding")); err == nil {
				err = newGRPCError(grpcInternal, "cardinality violation: received multiple request messages for non-client-streaming RPC")
			} else if errors.Is(err, io.EOF) {
				err = nil
			}
		}
	}
	if err != nil {
		stream.finish(grpcStatusOf(err))
		return
	}
	requestJSON, unknown, err := unmarshalGRPCRequest(data, method != grpcCapabilitiesMethod)
	if err != nil {
		if method == grpcWatchMethod {
			stream.finish(newGRPCError(grpcInternal, "grpc: failed to unmarshal the received message: %v", err))
		} else {
			stream.finish(newGRPCError(grpcInternal, "grpc: error unmarshalling request: %v", err))
		}
		return
	}
	if method == grpcWatchMethod {
		stream.finish(grpcStatusOf(g.watch(ctx, stream, requestJSON, unknown)))
		return
	}
	var response *grpcResponse
	if method == grpcCallMethod {
		response, err = g.call(ctx, requestJSON, unknown)
	} else {
		response, err = g.capabilities(ctx, unknown)
	}
	if err == nil {
		err = stream.send(response.marshal())
	}
	stream.finish(grpcStatusOf(err))
}

// grpcUnknownMethod is grpc-go's status for a path it does not serve.
func grpcUnknownMethod(path string) *grpcError {
	name, ok := strings.CutPrefix(path, "/")
	position := strings.LastIndex(name, "/")
	if !ok || position == -1 {
		return newGRPCError(grpcUnimplemented, "malformed method name: %q", path)
	}
	if service := name[:position]; service != grpcService {
		return newGRPCError(grpcUnimplemented, "unknown service %v", service)
	}
	return newGRPCError(grpcUnimplemented, "unknown method %v for service %v", name[position+1:], grpcService)
}

func (g *grpcGateway) call(ctx context.Context, requestJSON []byte, unknown bool) (*grpcResponse, error) {
	author, ok := grpcAuthor(ctx)
	if !ok {
		return nil, grpcStatus(store.ErrInvalid)
	}
	if unknown || len(requestJSON) == 0 || len(requestJSON) > grpcMaxRecv {
		return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
	}
	result, err := g.d.Submit(ctx, author, requestJSON, time.Now())
	return grpcAuthorReply(ctx, result, err)
}

func (g *grpcGateway) capabilities(ctx context.Context, unknown bool) (*grpcResponse, error) {
	author, ok := grpcAuthor(ctx)
	if !ok {
		return nil, grpcStatus(store.ErrInvalid)
	}
	if unknown {
		return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
	}
	result, err := g.d.Submit(ctx, author, []byte(`{"version":1,"request_id":"capabilities","operation":"capabilities.get"}`), time.Now())
	return grpcAuthorReply(ctx, result, err)
}

func (g *grpcGateway) watch(ctx context.Context, stream *grpcStream, requestJSON []byte, unknown bool) error {
	author, ok := grpcAuthor(ctx)
	if !ok || unknown || len(requestJSON) == 0 || len(requestJSON) > grpcMaxRecv {
		return grpcStatus(store.ErrInvalid)
	}
	if sub, err := core.DecodeSubmission(requestJSON); err != nil || !sub.Request.Operation.IsWatch() {
		return grpcStatus(store.ErrInvalid)
	}
	frames, bytesWritten := 0, 0
	const terminalReserve = MaxResponseFrameBytes + 5
	var last []byte
	send := func(frame *grpcWatchFrame) error {
		if err := grpcSetWriteDeadline(ctx, 3*time.Second); err != nil {
			return err
		}
		return stream.send(frame.marshal())
	}
	emit := func(page backend.HistoryPage) error {
		data, err := json.Marshal(page)
		frame := &grpcWatchFrame{PageJSON: data}
		wireBytes := len(frame.marshal()) + 5
		if err != nil || wireBytes > MaxResponseFrameBytes || frames >= MaxStreamFrames || bytesWritten+wireBytes+terminalReserve > MaxStreamBytes {
			return store.ErrCapacity
		}
		if err := send(frame); err != nil {
			return err
		}
		last = append(last[:0], data...)
		bytesWritten += wireBytes
		frames++
		return nil
	}
	err := g.d.SubmitWatch(ctx, author, requestJSON, time.Now(), emit)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reason := "resume"
	if errors.Is(err, backend.ErrStreamRenew) {
		reason = "lease_expired"
	} else if errors.Is(err, store.ErrCapacity) {
		reason = "capacity"
	} else if err != nil {
		reason = "upstream_unavailable"
	}
	terminal := &grpcTerminal{PageJSON: last, Reason: reason}
	if err != nil && len(last) == 0 {
		terminal.Reason = ""
		terminal.Error = grpcFailure(err)
	}
	terminalFrame := &grpcWatchFrame{Terminal: terminal}
	if size := len(terminalFrame.marshal()) + 5; size > MaxResponseFrameBytes || bytesWritten+size > MaxStreamBytes {
		terminalFrame = &grpcWatchFrame{Terminal: &grpcTerminal{Error: grpcFailure(store.ErrCapacity)}}
	}
	return send(terminalFrame)
}

// grpcAuthorReply answers a plain call with its DetachedResult JSON, the same
// object every carrier returns, or a typed failure (with AIRC's numeric for
// refused account calls).
func grpcAuthorReply(ctx context.Context, result dispatch.DetachedResult, err error) (*grpcResponse, error) {
	if err != nil {
		if code := grpcTransportError(err); code != nil {
			return nil, code
		}
		_, failure := errorFailure(err)
		return grpcReply(ctx, &grpcResponse{Error: &grpcFailureMessage{Code: string(failure.Code), Transient: failure.Transient, UpstreamCode: int32(failure.UpstreamCode)}})
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > grpcMaxSend {
		return grpcReply(ctx, grpcFailureResponse(store.ErrCapacity))
	}
	return grpcReply(ctx, &grpcResponse{ResultJSON: data})
}

func grpcFailure(err error) *grpcFailureMessage {
	_, code := httpError(err)
	return &grpcFailureMessage{Code: string(code), Transient: errors.Is(err, store.ErrCapacity) || errors.Is(err, backend.ErrReadUnavailable)}
}

func grpcFailureResponse(err error) *grpcResponse {
	return &grpcResponse{Error: grpcFailure(err)}
}

func grpcStatus(err error) error {
	switch {
	case errors.Is(err, auth.ErrAuthentication):
		return newGRPCError(grpcUnauthenticated, "authentication failed")
	case errors.Is(err, auth.ErrForbidden):
		return newGRPCError(grpcPermissionDenied, "forbidden")
	case errors.Is(err, store.ErrCapacity):
		return newGRPCError(grpcResourceExhausted, "capacity exceeded")
	case errors.Is(err, store.ErrInvalid):
		return newGRPCError(grpcInvalidArgument, "invalid request")
	case errors.Is(err, dispatch.ErrUnsupported):
		return newGRPCError(grpcUnimplemented, "unsupported operation")
	default:
		return newGRPCError(grpcUnavailable, "upstream unavailable")
	}
}

func grpcTransportError(err error) error {
	switch {
	case errors.Is(err, auth.ErrAuthentication), errors.Is(err, auth.ErrForbidden), errors.Is(err, store.ErrCapacity):
		return grpcStatus(err)
	default:
		return nil
	}
}
