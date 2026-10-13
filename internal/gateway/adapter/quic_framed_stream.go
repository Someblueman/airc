package adapter

import (
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// Raw bidi streams and paired uni streams expose the same framed directions,
// over QUIC (quic.StreamErrorCode) or WebTransport (wt.StreamErrorCode).
type quicReceiveStream[E ~uint32 | ~uint64] interface {
	Read([]byte) (int, error)
	CancelRead(E)
	SetReadDeadline(time.Time) error
}

type quicSendStream interface {
	Write([]byte) (int, error)
	Close() error
	SetWriteDeadline(time.Time) error
}

// quicFramedStream owns both directions of a framed stream: Close cancels the
// receiving side as well, to interrupt a blocked read on shutdown. A bidi
// stream is both recv and send.
type quicFramedStream[E ~uint32 | ~uint64] struct {
	recv     quicReceiveStream[E]
	send     quicSendStream
	in       *dispatch.Ingress
	mu       sync.Mutex
	writes   sync.Mutex
	closed   bool
	once     sync.Once
	closeErr error
}

func (s *quicFramedStream[E]) Read(p []byte) (int, error) { return s.recv.Read(p) }

func (s *quicFramedStream[E]) ingress() *dispatch.Ingress { return s.in }

func (s *quicFramedStream[E]) Write(p []byte) (int, error) {
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

func (s *quicFramedStream[E]) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		// WebTransport's reader can await session closure after the QUIC read
		// ends, which CancelRead alone does not interrupt; the expired deadline
		// does, and holding mu stops a concurrent framed read restoring it.
		_ = s.recv.SetReadDeadline(time.Now())
		_ = s.send.SetWriteDeadline(time.Now())
		s.mu.Unlock()
		s.recv.CancelRead(0)
		// quic-go forbids concurrent SendStream.Close and Write. Expiring the
		// guarded deadline interrupts a blocked write before issuing normal FIN.
		s.writes.Lock()
		s.closeErr = s.send.Close()
		s.writes.Unlock()
	})
	return s.closeErr
}

func (s *quicFramedStream[E]) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.recv.SetReadDeadline(t)
}

func (s *quicFramedStream[E]) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.send.SetWriteDeadline(t)
}
