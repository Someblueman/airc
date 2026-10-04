package adapter

import (
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// Raw bidi streams and paired uni streams expose the same framed directions.
type quicReceiveStream interface {
	Read([]byte) (int, error)
	CancelRead(quic.StreamErrorCode)
	SetReadDeadline(time.Time) error
}

type quicSendStream interface {
	Write([]byte) (int, error)
	Close() error
	SetWriteDeadline(time.Time) error
}

type quicFramedStream struct {
	recv     quicReceiveStream
	send     quicSendStream
	mu       sync.Mutex
	writes   sync.Mutex
	closed   bool
	once     sync.Once
	closeErr error
}

func (s *quicFramedStream) Read(p []byte) (int, error) { return s.recv.Read(p) }

func (s *quicFramedStream) Write(p []byte) (int, error) {
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

func (s *quicFramedStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
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

func (s *quicFramedStream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.recv.SetReadDeadline(t)
}

func (s *quicFramedStream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	return s.send.SetWriteDeadline(t)
}
