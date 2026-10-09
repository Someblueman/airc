package adapter

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/quic-go/quic-go"
)

const QUICALPN = "airc-gateway-quic/1"

type QUIC struct {
	d         *dispatch.Dispatcher
	requests  chan struct{}
	datagrams bool
	streams   chan struct{}
}

func QUICServer(d *dispatch.Dispatcher, requests chan struct{}, datagrams bool) (*QUIC, error) {
	if d == nil || requests == nil || cap(requests) < 1 || cap(requests) > 256 {
		return nil, store.ErrInvalid
	}
	return &QUIC{d: d, requests: requests, datagrams: datagrams, streams: make(chan struct{}, cap(requests))}, nil
}

func (q *QUIC) ServeConn(parent context.Context, conn *quic.Conn) error {
	if q == nil || conn == nil {
		return errors.New("nil QUIC adapter or connection")
	}
	if q.datagrams && !conn.ConnectionState().SupportsDatagrams.Remote {
		return errors.New("QUIC datagrams not negotiated")
	}
	ctx, cancel := context.WithCancel(parent)
	connDone := make(chan struct{})
	stopConn := context.AfterFunc(conn.Context(), func() { cancel(); close(connDone) })
	defer func() {
		cancel()
		if !stopConn() {
			<-connDone
		}
	}()
	var streams sync.WaitGroup
	var accept sync.WaitGroup
	accept.Add(2)
	go func() { defer accept.Done(); q.acceptBidi(ctx, conn, &streams) }()
	go func() { defer accept.Done(); q.acceptUni(ctx, conn, &streams) }()
	var datagramDone chan struct{}
	if q.datagrams {
		datagramDone = make(chan struct{})
		go func() {
			defer close(datagramDone)
			if err := serveAGD1Datagrams(ctx, conn, func() { _ = conn.CloseWithError(0, "QUIC datagram write bound") }, q.d, q.requests, "quic_datagrams", nil); err != nil {
				cancel()
			}
		}()
	}
	acceptDone := make(chan struct{})
	go func() { accept.Wait(); close(acceptDone) }()
	select {
	case <-ctx.Done():
	case <-acceptDone:
	case <-datagramDone:
		cancel()
	}
	cancel()
	_ = conn.CloseWithError(0, "QUIC connection closed")
	<-acceptDone
	if datagramDone != nil {
		<-datagramDone
	}
	streams.Wait()
	return nil
}

func joinedQUICContext(parent, child context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	childDone := make(chan struct{})
	stop := context.AfterFunc(child, func() { cancel(); close(childDone) })
	return ctx, func() {
		cancel()
		if !stop() {
			<-childDone
		}
	}
}

func (q *QUIC) acceptBidi(ctx context.Context, conn *quic.Conn, work *sync.WaitGroup) {
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		select {
		case q.streams <- struct{}{}:
		default:
			stream.CancelRead(0)
			stream.CancelWrite(0)
			continue
		}
		work.Add(1)
		go func() {
			defer work.Done()
			defer func() { <-q.streams }()
			streamCtx, finish := joinedQUICContext(ctx, stream.Context())
			defer finish()
			_ = serveFramedStreamTransport(streamCtx, &quicFramedStream{recv: stream, send: stream}, q.d, q.requests, false, false, true, []string{"long_poll", "quic_stream"}, 3*time.Second)
		}()
	}
}

func (q *QUIC) acceptUni(ctx context.Context, conn *quic.Conn, work *sync.WaitGroup) {
	for {
		stream, err := conn.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		select {
		case q.streams <- struct{}{}:
		default:
			stream.CancelRead(0)
			continue
		}
		work.Add(1)
		go func() {
			defer work.Done()
			defer func() { <-q.streams }()
			openCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			out, err := conn.OpenUniStreamSync(openCtx)
			cancel()
			if err != nil {
				stream.CancelRead(0)
				return
			}
			streamCtx, finish := joinedQUICContext(ctx, out.Context())
			defer finish()
			_ = serveFramedStreamTransport(streamCtx, &quicFramedStream{recv: stream, send: out}, q.d, q.requests, false, false, true, []string{"long_poll", "quic_uni_stream"}, 3*time.Second)
		}()
	}
}
