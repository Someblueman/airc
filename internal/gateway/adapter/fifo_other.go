//go:build !darwin && !linux

package adapter

import (
	"errors"
	"os"
	"time"
)

type FIFOStream struct{}

func OpenFIFOStream(string) (*FIFOStream, error) {
	return nil, errors.New("paired FIFO adapter unavailable on this platform")
}
func (*FIFOStream) Read([]byte) (int, error)         { return 0, os.ErrInvalid }
func (*FIFOStream) Write([]byte) (int, error)        { return 0, os.ErrInvalid }
func (*FIFOStream) SetReadDeadline(time.Time) error  { return os.ErrInvalid }
func (*FIFOStream) SetWriteDeadline(time.Time) error { return os.ErrInvalid }
func (*FIFOStream) Close() error                     { return nil }
