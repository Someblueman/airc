package adapter

import (
	"context"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	wt "github.com/quic-go/webtransport-go"
)

func (a *WebTransport) acceptBidi(ctx context.Context, session *wt.Session, work *sync.WaitGroup, plain *dispatch.Ingress) {
	for {
		stream, err := session.AcceptStream(ctx)
		if err != nil {
			return
		}
		select {
		case a.streams <- struct{}{}:
		default:
			stream.CancelRead(0)
			stream.CancelWrite(0)
			continue
		}
		work.Go(func() {
			defer func() { <-a.streams }()
			a.serveStream(ctx, stream.Context(), &quicFramedStream[wt.StreamErrorCode]{recv: stream, send: stream, in: plain}, "webtransport_stream")
		})
	}
}

func (a *WebTransport) acceptUni(ctx context.Context, session *wt.Session, work *sync.WaitGroup, plain *dispatch.Ingress) {
	for {
		stream, err := session.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		select {
		case a.streams <- struct{}{}:
		default:
			stream.CancelRead(0)
			continue
		}
		work.Go(func() {
			defer func() { <-a.streams }()
			openCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			out, err := session.OpenUniStreamSync(openCtx)
			if err != nil {
				stream.CancelRead(0)
				return
			}
			a.serveStream(ctx, out.Context(), &quicFramedStream[wt.StreamErrorCode]{recv: stream, send: out, in: plain}, "webtransport_uni_stream")
		})
	}
}

// A peer STOP_SENDING cancels the outgoing stream context even when the
// request side has already ended cleanly. Join that cancellation callback so a
// quiet watch releases its native lease without waiting for another write.
func (a *WebTransport) serveStream(parent, outgoing context.Context, stream FramedStream, mode string) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	stop := context.AfterFunc(outgoing, func() { cancel(); close(done) })
	defer func() {
		cancel()
		if !stop() {
			<-done
		}
	}()
	_ = serveFramedStreamTransport(ctx, stream, a.d, a.requests, false, true, []string{"long_poll", mode}, 3*time.Second)
}
