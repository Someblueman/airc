package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	gatewayv1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	grpcMaxRecv    = core.MaxEnvelopeBytes + 1024
	grpcMaxSend    = 1 << 20
	grpcWatchLease = 20 * time.Second
)

type grpcHTTPWriterKey struct{}

// grpcAuthorKey carries the ingress of a call without bearer metadata: its
// envelope is a plain contract request run as its author.
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

func grpcReply(ctx context.Context, response *gatewayv1.Response) (*gatewayv1.Response, error) {
	if err := grpcSetWriteDeadline(ctx, 3*time.Second); err != nil {
		return nil, status.Error(codes.Unavailable, "response unavailable")
	}
	if proto.Size(response)+5 > grpcMaxSend {
		response = grpcFailureResponse(store.ErrCapacity)
	}
	return response, nil
}

// GRPCServer returns an HTTP/2-only server whose handler is the generated
// gRPC service. The service owns listener, optional TLS and shutdown
// lifecycle. A call without bearer metadata is a plain author request and,
// as on IRC, needs no TLS (cleartext HTTP/2 prior knowledge, h2c); bearer
// principals keep TLS.
func GRPCServer(d *dispatch.Dispatcher, requests chan struct{}) (*http.Server, error) {
	if d == nil || requests == nil || cap(requests) < 1 || cap(requests) > 256 {
		return nil, store.ErrInvalid
	}
	s := grpc.NewServer(grpc.MaxRecvMsgSize(grpcMaxRecv), grpc.MaxSendMsgSize(grpcMaxSend))
	gatewayv1.RegisterGatewayServer(s, &grpcGateway{d: d})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plain := len(r.Header.Values("Authorization")) == 0 && d.AuthorAccess()
		if r.TLS == nil && !plain || r.ProtoMajor != 2 || r.URL.RawQuery != "" || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("X-Forwarded-Host")) != 0 || len(r.Header.Values("X-Original-Host")) != 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), grpcWatchLease)
		defer cancel()
		ctx = context.WithValue(ctx, grpcHTTPWriterKey{}, http.NewResponseController(w))
		defer func() { _ = grpcSetWriteDeadline(ctx, 5*time.Second) }()
		if err := grpcSetWriteDeadline(ctx, 23*time.Second); err != nil {
			writeGRPCHTTPStatus(w, status.Error(codes.Unavailable, "response unavailable"))
			return
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		default:
			writeGRPCHTTPStatus(w, status.Error(codes.ResourceExhausted, "capacity exceeded"))
			return
		}
		if plain {
			ctx = context.WithValue(ctx, grpcAuthorKey{}, dispatch.Ingress{Source: d.RemoteSource(r.RemoteAddr)})
			s.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		token, err := grpcHTTPBearer(r)
		if err != nil {
			writeGRPCHTTPStatus(w, grpcStatus(err))
			return
		}
		authCtx, finishAuth := context.WithTimeout(ctx, 5*time.Second)
		_, authErr := d.Capabilities(authCtx, token)
		finishAuth()
		if authErr != nil {
			writeGRPCHTTPStatus(w, grpcStatus(authErr))
			return
		}
		s.ServeHTTP(w, r.WithContext(ctx))
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
	gatewayv1.UnimplementedGatewayServer
	d *dispatch.Dispatcher
}

func grpcBearer(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", auth.ErrAuthentication
	}
	values := md.Get("authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) != len("Bearer ")+64 {
		return "", auth.ErrAuthentication
	}
	return values[0][len("Bearer "):], nil
}

func grpcHTTPBearer(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) != len("Bearer ")+64 {
		return "", auth.ErrAuthentication
	}
	return values[0][len("Bearer "):], nil
}

func writeGRPCHTTPStatus(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		st = status.New(codes.Unavailable, "upstream unavailable")
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	w.Header().Set("Grpc-Status", strconv.Itoa(int(st.Code())))
	w.Header().Set("Grpc-Message", st.Message())
	w.WriteHeader(http.StatusOK)
}

func grpcAdmission(ctx context.Context, d *dispatch.Dispatcher) (string, error) {
	token, err := grpcBearer(ctx)
	if err != nil {
		return "", err
	}
	if _, err := d.Capabilities(ctx, token); err != nil {
		return "", err
	}
	return token, nil
}

func (g *grpcGateway) Call(ctx context.Context, in *gatewayv1.Request) (*gatewayv1.Response, error) {
	if author, ok := grpcAuthor(ctx); ok {
		if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 || len(in.GetEnvelope()) == 0 || len(in.GetEnvelope()) > grpcMaxRecv {
			return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
		}
		result, err := g.d.Submit(ctx, author, in.GetEnvelope(), time.Now())
		return grpcAuthorReply(ctx, result, err)
	}
	token, err := grpcAdmission(ctx, g.d)
	if err != nil {
		return nil, grpcStatus(err)
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 || len(in.GetEnvelope()) == 0 || len(in.GetEnvelope()) > grpcMaxRecv {
		return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
	}
	req, err := core.Decode(in.GetEnvelope())
	if err != nil {
		return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
	}
	value, err := bearerCall(ctx, g.d, token, req.Operation, in.GetEnvelope())
	if err != nil {
		if code := grpcTransportError(err); code != nil {
			return nil, code
		}
		return grpcReply(ctx, grpcFailureResponse(err))
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > grpcMaxSend {
		return grpcReply(ctx, grpcFailureResponse(store.ErrCapacity))
	}
	return grpcReply(ctx, &gatewayv1.Response{Outcome: &gatewayv1.Response_ResultJson{ResultJson: data}})
}

func (g *grpcGateway) Capabilities(ctx context.Context, in *gatewayv1.Empty) (*gatewayv1.Response, error) {
	if author, ok := grpcAuthor(ctx); ok {
		if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 {
			return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
		}
		result, err := g.d.Submit(ctx, author, []byte(`{"version":1,"request_id":"capabilities","operation":"capabilities.get"}`), time.Now())
		return grpcAuthorReply(ctx, result, err)
	}
	token, err := grpcAdmission(ctx, g.d)
	if err != nil {
		return nil, grpcStatus(err)
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 {
		return grpcReply(ctx, grpcFailureResponse(store.ErrInvalid))
	}
	value, err := g.d.Capabilities(ctx, token)
	if err != nil {
		return nil, grpcStatus(err)
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > grpcMaxSend {
		return grpcReply(ctx, grpcFailureResponse(store.ErrCapacity))
	}
	return grpcReply(ctx, &gatewayv1.Response{Outcome: &gatewayv1.Response_ResultJson{ResultJson: data}})
}

func (g *grpcGateway) Watch(in *gatewayv1.Request, stream grpc.ServerStreamingServer[gatewayv1.WatchFrame]) error {
	author, plain := grpcAuthor(stream.Context())
	var token string
	if !plain {
		var err error
		if token, err = grpcAdmission(stream.Context(), g.d); err != nil {
			return grpcStatus(err)
		}
	}
	if in == nil || len(in.ProtoReflect().GetUnknown()) != 0 || len(in.GetEnvelope()) == 0 || len(in.GetEnvelope()) > grpcMaxRecv {
		return grpcStatus(store.ErrInvalid)
	}
	if plain {
		sub, err := core.DecodeSubmission(in.GetEnvelope())
		if err != nil || !sub.Request.Operation.IsWatch() {
			return grpcStatus(store.ErrInvalid)
		}
	} else if req, err := core.Decode(in.GetEnvelope()); err != nil || !req.Operation.IsWatch() {
		return grpcStatus(store.ErrInvalid)
	}
	ctx := stream.Context()
	frames, bytesWritten := 0, 0
	const terminalReserve = MaxResponseFrameBytes + 5
	var last []byte
	send := func(frame *gatewayv1.WatchFrame) error {
		if err := grpcSetWriteDeadline(ctx, 3*time.Second); err != nil {
			return err
		}
		return stream.Send(frame)
	}
	emit := func(page backend.HistoryPage) error {
		data, err := json.Marshal(page)
		frame := &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_PageJson{PageJson: data}}
		wireBytes := proto.Size(frame) + 5
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
	var err error
	if plain {
		err = g.d.SubmitWatch(ctx, author, in.GetEnvelope(), time.Now(), emit)
	} else {
		_, err = g.d.ReadPage(ctx, token, in.GetEnvelope(), "", time.Now(), emit)
	}
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
	terminal := &gatewayv1.Terminal{PageJson: last, Reason: reason}
	if err != nil && len(last) == 0 {
		terminal.Reason = ""
		terminal.Error = grpcFailure(err)
	}
	terminalFrame := &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_Terminal{Terminal: terminal}}
	if proto.Size(terminalFrame)+5 > MaxResponseFrameBytes || bytesWritten+proto.Size(terminalFrame)+5 > MaxStreamBytes {
		terminal = &gatewayv1.Terminal{Error: grpcFailure(store.ErrCapacity)}
		terminalFrame = &gatewayv1.WatchFrame{Event: &gatewayv1.WatchFrame_Terminal{Terminal: terminal}}
	}
	return send(terminalFrame)
}

// grpcAuthorReply answers a plain call with its DetachedResult JSON, the same
// object every carrier returns, or a typed failure (with AIRC's numeric for
// refused account calls).
func grpcAuthorReply(ctx context.Context, result dispatch.DetachedResult, err error) (*gatewayv1.Response, error) {
	if err != nil {
		if code := grpcTransportError(err); code != nil {
			return nil, code
		}
		_, failure := errorFailure(err)
		return grpcReply(ctx, &gatewayv1.Response{Outcome: &gatewayv1.Response_Error{Error: &gatewayv1.Failure{Code: string(failure.Code), Transient: failure.Transient, UpstreamCode: int32(failure.UpstreamCode)}}})
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > grpcMaxSend {
		return grpcReply(ctx, grpcFailureResponse(store.ErrCapacity))
	}
	return grpcReply(ctx, &gatewayv1.Response{Outcome: &gatewayv1.Response_ResultJson{ResultJson: data}})
}

func grpcFailure(err error) *gatewayv1.Failure {
	_, code := httpError(err)
	return &gatewayv1.Failure{Code: string(code), Transient: errors.Is(err, store.ErrCapacity) || errors.Is(err, backend.ErrReadUnavailable)}
}

func grpcFailureResponse(err error) *gatewayv1.Response {
	return &gatewayv1.Response{Outcome: &gatewayv1.Response_Error{Error: grpcFailure(err)}}
}

func grpcStatus(err error) error {
	switch {
	case errors.Is(err, auth.ErrAuthentication):
		return status.Error(codes.Unauthenticated, "authentication failed")
	case errors.Is(err, auth.ErrForbidden):
		return status.Error(codes.PermissionDenied, "forbidden")
	case errors.Is(err, store.ErrCapacity):
		return status.Error(codes.ResourceExhausted, "capacity exceeded")
	case errors.Is(err, store.ErrInvalid):
		return status.Error(codes.InvalidArgument, "invalid request")
	case errors.Is(err, dispatch.ErrUnsupported):
		return status.Error(codes.Unimplemented, "unsupported operation")
	default:
		return status.Error(codes.Unavailable, "upstream unavailable")
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
