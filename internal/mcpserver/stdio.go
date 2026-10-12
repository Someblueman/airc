package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
)

// ServeStdio serves one session of newline-delimited JSON-RPC from r to w
// until r ends or ctx is done. Calls run concurrently, except initialize.
// Either way, outstanding calls are cancelled and joined first. End of input
// returns nil; cancellation returns ctx's error.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	session := s.NewSession()
	var mu sync.Mutex
	write := func(data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		_, err := w.Write(append(data, '\n'))
		return err
	}
	notify := func(_ context.Context, data []byte) error { return write(data) }
	var calls sync.WaitGroup
	defer calls.Wait()

	type line struct {
		data []byte
		err  error
	}
	lines := make(chan line)
	// The reader may stay blocked after cancellation; it never touches w.
	go func() {
		reader := bufio.NewReader(r)
		for {
			data, err := reader.ReadBytes('\n')
			select {
			case lines <- line{data, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var next line
		select {
		case <-ctx.Done():
			return ctx.Err()
		case next = <-lines:
		}
		if data := bytes.TrimSpace(next.data); len(data) > 0 {
			m, err := ParseMessage(data)
			switch {
			case err != nil:
				_ = write(encodeError(nil, CodeParseError, "parse error: "+err.Error()))
			case m.IsCall() && m.Method != "initialize":
				calls.Go(func() {
					if response := session.Handle(ctx, m, notify); response != nil {
						_ = write(response)
					}
				})
			default:
				if response := session.Handle(ctx, m, notify); response != nil {
					_ = write(response)
				}
			}
		}
		if next.err != nil {
			cancel()
			if errors.Is(next.err, io.EOF) {
				return nil
			}
			return next.err
		}
	}
}
