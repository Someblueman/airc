package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// SCTPMode selects the native record delivery profile. Each profile has a
// separate configured protocol and protected return route.
type SCTPMode uint8

const (
	SCTPOrdered SCTPMode = iota
	SCTPUnordered
	SCTPOrderedTTL
)

// ServeSCTP owns and joins accepted message associations. The caller
// bounds connections. ListenSCTP connections preserve one complete native record
// per Read/Write; they must not be replaced with byte-stream connections.
func ServeSCTP(parent context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}, protection *ConnectionResponseProtection, mode SCTPMode) error {
	if mode > SCTPOrderedTTL || listener == nil || d == nil || cap(requests) < 1 || protection != nil && (protection.sealer == nil || len(protection.routes) == 0) || protection == nil && !d.AuthorAccess() {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	var connections sync.WaitGroup
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	defer func() {
		cancel()
		_ = listener.Close()
		if !stop() {
			<-closed
		}
		connections.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		connections.Go(func() { _ = serveSCTPConnection(ctx, conn, d, requests, protection, mode) })
	}
}

func serveSCTPConnection(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, protection *ConnectionResponseProtection, mode SCTPMode) error {
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		cancel()
		_ = conn.Close()
		if !stop() {
			<-closed
		}
	}()
	// One author session per association; the source is the peer's IP.
	plain := authorIngress(d, conn.RemoteAddr(), true)
	defer closeIngress(plain)
	body := make([]byte, MaxRequestFrameBytes)
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		idle := 30 * time.Second
		if first {
			idle = 5 * time.Second
		}
		if err := conn.SetReadDeadline(time.Now().Add(idle)); err != nil {
			return err
		}
		n, err := conn.Read(body)
		if err != nil {
			return err
		}
		first = false
		if n < 1 || n > len(body) {
			return store.ErrInvalid
		}
		request, plainBody, err := decodeFrame(body[:n])
		if err != nil || request.Version != 1 || !correlationID(request.CorrelationID) {
			return store.ErrInvalid
		}
		if request.Profile == "plain" {
			// A plain frame carries its contract request; results are plain
			// frames on the association, like every other carrier.
			if plainBody == nil || request.Envelope != "" || plain == nil {
				return store.ErrInvalid
			}
			if err := runSCTPPlainRequest(ctx, conn, d, requests, request, *plain, plainBody, mode); err != nil {
				return err
			}
			continue
		}
		if protection == nil || plainBody != nil || request.Profile != "encrypted" || request.Envelope == "" {
			return store.ErrInvalid
		}
		verified, err := d.AuthenticateDetached(request.Envelope, true, time.Now())
		if err != nil {
			return err
		}
		// The outer label is otherwise mutable by a relay. Bind replies to
		// the exact authenticated envelope, including reads without receipts.
		digest := sha256.Sum256([]byte(request.Envelope))
		if request.CorrelationID != hex.EncodeToString(digest[:]) {
			return store.ErrInvalid
		}
		// A connected peer cannot nominate an identity or a response key. Refuse
		// requests without a configured protected route before any admission.
		if _, ok := protection.routes[verified.PrincipalID()+"\x00"+verified.Binding()]; !ok {
			return store.ErrInvalid
		}
		if err := runSCTPRequest(ctx, conn, d, requests, protection, request, verified, mode); err != nil {
			return err
		}
	}
}

func sctpWatchMode(mode SCTPMode) string {
	switch mode {
	case SCTPUnordered:
		return "sctp_unordered_records"
	case SCTPOrderedTTL:
		return "sctp_pr_ttl_records"
	}
	return "sctp_ordered_records"
}

// runSCTPPlainRequest answers a plain frame with plain sequenced frames, one
// record each, within the same stream budget as protected results.
func runSCTPPlainRequest(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, request FrameRequest, in dispatch.Ingress, body []byte, mode SCTPMode) error {
	timeout := 4 * time.Second
	if request.Stream {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var wireBytes int
	write := func(response FrameResponse) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := json.Marshal(response)
		if err != nil || len(record) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		limit := MaxStreamBytes - (4 << 10)
		if response.Event == "error" || response.Event == "end" {
			limit = MaxStreamBytes
		}
		if wireBytes+len(record) > limit {
			return store.ErrCapacity
		}
		deadline := time.Now().Add(3 * time.Second)
		if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
			deadline = end
		}
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		n, err := conn.Write(record)
		if err == nil && n != len(record) {
			return io.ErrShortWrite
		}
		wireBytes += n
		return err
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return writeCorrelatedFailure(func(response FrameResponse) error {
			response.Sequence = 1
			return write(response)
		}, request.CorrelationID, store.ErrCapacity)
	}
	var writeFailure error
	runCorrelatedPlainRequest(ctx, func(response FrameResponse) error {
		err := write(response)
		if err != nil && !errors.Is(err, store.ErrCapacity) {
			writeFailure = err
			cancel()
		}
		return err
	}, []string{"long_poll", sctpWatchMode(mode)}, true, d, request, in, body)
	return writeFailure
}

func runSCTPRequest(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, protection *ConnectionResponseProtection, request FrameRequest, verified dispatch.VerifiedMessage, mode SCTPMode) error {
	timeout := 4 * time.Second
	if request.Stream {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	write := sctpResponseWriter(ctx, conn, protection, verified)
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return writeCorrelatedFailure(func(response FrameResponse) error {
			response.Sequence = 1
			return write(response)
		}, request.CorrelationID, store.ErrCapacity)
	}
	var writeFailure error
	watchMode := sctpWatchMode(mode)
	runCorrelatedVerifiedRequest(ctx, func(response FrameResponse) error {
		err := write(response)
		if err != nil && !errors.Is(err, store.ErrCapacity) {
			writeFailure = err
			cancel()
		}
		return err
	}, []string{"long_poll", watchMode}, true, d, request, verified)
	return writeFailure
}

func sctpResponseWriter(ctx context.Context, conn net.Conn, protection *ConnectionResponseProtection, verified dispatch.VerifiedMessage) func(FrameResponse) error {
	var wireBytes int
	return func(response FrameResponse) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := protection.seal(verified, response)
		if errors.Is(err, auth.ErrResponseCapacity) {
			return store.ErrCapacity
		}
		if err != nil {
			return err
		}
		limit := MaxStreamBytes - (4 << 10)
		if response.Event == "error" || response.Event == "end" {
			limit = MaxStreamBytes
		}
		if wireBytes+len(token) > limit {
			return store.ErrCapacity
		}
		deadline := time.Now().Add(3 * time.Second)
		if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
			deadline = end
		}
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		n, err := conn.Write([]byte(token))
		if err == nil && n != len(token) {
			return io.ErrShortWrite
		}
		if err == nil {
			wireBytes += n
		}
		return err
	}
}
