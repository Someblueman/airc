package adapter

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// ServeOSCORERPC owns one private socket from the configured native child. The
// child must authenticate and persist OSCORE state before sending a request.
// The owner is fixed at startup from the private context file, never from a
// packet. The caller owns and joins the child and validates the owner's realm.
// Requests are sequential and bounded; persistent delivery IDs and bounded
// history cursors retain their existing dispatcher semantics. Streaming is
// refused. Every envelope must also verify under the same principal/binding.
func ServeOSCORERPC(ctx context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, owner auth.OSCOREOwner) (result error) {
	if conn == nil || d == nil || cap(requests) < 1 || cap(requests) > 256 || owner.Realm == "" || owner.Principal == "" || owner.Binding == "" {
		return store.ErrInvalid
	}
	defer func() {
		_ = conn.Close()
		if ctx.Err() != nil {
			result = nil
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	for {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		}
		var header [4]byte
		_, err := io.ReadFull(conn, header[:1])
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, header[1:]); err != nil {
			return err
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > MaxRequestFrameBytes {
			return store.ErrInvalid
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		code, response := oscoreRPCRequest(ctx, body, d, requests, owner)
		clear(body)
		encoded, err := json.Marshal(response)
		if err != nil || len(encoded) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		var reply [5]byte
		reply[0] = byte(code)
		binary.BigEndian.PutUint32(reply[1:], uint32(len(encoded)))
		if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return err
		}
		if _, err := io.Copy(conn, io.MultiReader(bytes.NewReader(reply[:]), bytes.NewReader(encoded))); err != nil {
			return err
		}
	}
}

func oscoreRPCRequest(ctx context.Context, body []byte, d *dispatch.Dispatcher, requests chan struct{}, owner auth.OSCOREOwner) (codes.Code, FrameResponse) {
	var request FrameRequest
	var response FrameResponse
	write := func(value FrameResponse) error {
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		response = value
		return nil
	}
	failure := func(code codes.Code, err error) (codes.Code, FrameResponse) {
		_ = writeCorrelatedFailure(write, request.CorrelationID, err)
		return code, response
	}
	if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Stream || (request.Profile != "signed" && request.Profile != "encrypted") || request.Envelope == "" {
		return failure(codes.BadRequest, store.ErrInvalid)
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return failure(codes.ServiceUnavailable, store.ErrCapacity)
	}
	verified, err := d.AuthenticateDetached(request.Envelope, request.Profile == "encrypted", time.Now())
	if err != nil {
		return failure(codes.Unauthorized, err)
	}
	if verified.PrincipalID() != owner.Principal || verified.Binding() != owner.Binding {
		return failure(codes.Unauthorized, auth.ErrAuthentication)
	}
	runCorrelatedVerifiedRequest(ctx, write, []string{"long_poll"}, true, d, request, verified)
	if response.Error != nil {
		return codes.BadRequest, response
	}
	return codes.Content, response
}
