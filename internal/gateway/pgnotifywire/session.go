package pgnotifywire

import (
	"context"
	"errors"
	"net"
)

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.closeErr = closeConnection(s.conn)
		s.clearPending()
	}
	err := s.closeErr
	s.mu.Unlock()
	// Close joins the active Next call after closing its connection.
	s.readMu.Lock()
	//lint:ignore SA2001 This lock is an operation completion barrier.
	s.readMu.Unlock()
	return err
}

func (s *Session) Next(ctx context.Context) ([]byte, error) {
	if s == nil || ctx == nil {
		return nil, ErrProtocol
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		s.closed = true
		s.closeErr = closeConnection(s.conn)
		s.clearPending()
		s.mu.Unlock()
		return nil, err
	}
	if len(s.pending) > 0 {
		payload := s.pending[0]
		s.pending[0] = nil
		s.pending = s.pending[1:]
		s.mu.Unlock()
		return payload, nil
	}
	conn := s.conn
	s.mu.Unlock()

	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	ignored := 0
	for {
		typ, body, err := readAsyncFrame(conn)
		if err != nil {
			if ctx.Err() != nil {
				s.markClosed()
				return nil, ctx.Err()
			}
			s.markClosed()
			return nil, ErrProtocol
		}
		switch typ {
		case 'A':
			payload, err := s.notification(body)
			clear(body)
			if err != nil || ctx.Err() != nil {
				clear(payload)
				s.markClosed()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, ErrProtocol
			}
			return payload, nil
		case 'N':
			err = s.notice(body)
		case 'S':
			err = s.parameterStatus(body)
		default:
			err = ErrProtocol
		}
		clear(body)
		ignored++
		if err != nil || ignored > 32 {
			s.markClosed()
			return nil, ErrProtocol
		}
	}
}

func (s *Session) markClosed() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.closeErr = closeConnection(s.conn)
		s.clearPending()
	}
	s.mu.Unlock()
}

func (s *Session) clearPending() {
	for _, payload := range s.pending {
		clear(payload)
	}
	s.pending = nil
}

func closeConnection(conn net.Conn) error {
	if conn == nil {
		return nil
	}
	err := conn.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
