//go:build !darwin && !linux

package adapter

import (
	"errors"
	"os"
	"time"
)

// PipeStream is unavailable on this platform; no stream can be constructed.
type PipeStream struct{}

func OpenPipeStream(*os.File, *os.File) (*PipeStream, error) {
	return nil, errors.New("stdio pipe adapter unavailable on this platform")
}
func (*PipeStream) Read([]byte) (int, error)         { return 0, os.ErrInvalid }
func (*PipeStream) Write([]byte) (int, error)        { return 0, os.ErrInvalid }
func (*PipeStream) SetReadDeadline(time.Time) error  { return os.ErrInvalid }
func (*PipeStream) SetWriteDeadline(time.Time) error { return os.ErrInvalid }
func (*PipeStream) Close() error                     { return nil }
