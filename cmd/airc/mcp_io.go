package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
)

// Bound each newline-delimited input before the SDK allocates a JSON message.
type mcpInputReader struct {
	source    io.ReadCloser
	reader    *bufio.Reader
	lineBytes int
	cancel    context.CancelFunc
	eof       atomic.Bool
}

func (r *mcpInputReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.eof.Store(true)
		}
		if r.cancel != nil {
			r.cancel()
		}
	}
	for _, b := range p[:n] {
		if b == '\n' {
			r.lineBytes = 0
		} else {
			r.lineBytes++
			if r.lineBytes > 1<<20 {
				if r.cancel != nil {
					r.cancel()
				}
				return 0, errors.New("MCP request exceeds 1 MiB")
			}
		}
	}
	return n, err
}
func (r *mcpInputReader) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	return r.source.Close()
}

type mcpWriter struct{ io.Writer }

func (mcpWriter) Close() error { return nil }

type boundedCapture struct {
	bytes.Buffer
	limit int
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("CLI output exceeded adapter limit")
	}
	return b.Buffer.Write(p)
}
