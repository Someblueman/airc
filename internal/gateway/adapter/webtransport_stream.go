package adapter

import (
	"context"
	"net"
	"sync"
	"time"

	wt "github.com/quic-go/webtransport-go"
)

func (a *WebTransport) acceptBidi(ctx context.Context, session *wt.Session, work *sync.WaitGroup) {
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
			a.serveStream(ctx, stream.Context(), &webTransportBiStream{stream: stream}, "webtransport_stream")
		})
	}
}

func (a *WebTransport) acceptUni(ctx context.Context, session *wt.Session, work *sync.WaitGroup) {
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
			a.serveStream(ctx, out.Context(), &webTransportUniStream{recv: stream, send: out}, "webtransport_uni_stream")
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
	_ = serveFramedStreamTransport(ctx, stream, a.d, a.requests, false, false, true, []string{"long_poll", mode}, 3*time.Second)
}

// Stream.Close closes only its sending side. The framed owner also owns the
// receiving side, and must cancel it to interrupt a blocked read on shutdown.
type webTransportBiStream struct {
	stream   *wt.Stream
	mu       sync.Mutex
	writes   sync.Mutex
	closed   bool
	once     sync.Once
	closeErr error
}

func (s *webTransportBiStream) Read(p []byte) (int, error) { return s.stream.Read(p) }
func (s *webTransportBiStream) Write(p []byte) (int, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	return s.stream.Write(p)
}
func (s *webTransportBiStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		// WT's SessionGone reader can await session closure after the underlying
		// QUIC read ends. CancelRead alone does not interrupt that internal wait.
		// Guard against a concurrent framed read restoring its normal idle deadline.
		_ = s.stream.SetReadDeadline(time.Now())
		_ = s.stream.SetWriteDeadline(time.Now())
		s.mu.Unlock()
		s.stream.CancelRead(0)
		// The underlying QUIC send stream forbids concurrent Close and Write.
		s.writes.Lock()
		s.closeErr = s.stream.Close()
		s.writes.Unlock()
	})
	return s.closeErr
}
func (s *webTransportBiStream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.stream.SetReadDeadline(t)
}
func (s *webTransportBiStream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.stream.SetWriteDeadline(t)
}

type webTransportUniStream struct {
	recv     *wt.ReceiveStream
	send     *wt.SendStream
	mu       sync.Mutex
	writes   sync.Mutex
	closed   bool
	once     sync.Once
	closeErr error
}

func (s *webTransportUniStream) Read(p []byte) (int, error) { return s.recv.Read(p) }
func (s *webTransportUniStream) Write(p []byte) (int, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	return s.send.Write(p)
}
func (s *webTransportUniStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		_ = s.recv.SetReadDeadline(time.Now())
		_ = s.send.SetWriteDeadline(time.Now())
		s.mu.Unlock()
		s.recv.CancelRead(0)
		s.writes.Lock()
		s.closeErr = s.send.Close()
		s.writes.Unlock()
	})
	return s.closeErr
}
func (s *webTransportUniStream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.recv.SetReadDeadline(t)
}
func (s *webTransportUniStream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.send.SetWriteDeadline(t)
}
