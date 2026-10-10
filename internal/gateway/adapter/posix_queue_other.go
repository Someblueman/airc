//go:build !linux || (!arm64 && !amd64)

package adapter

import "context"

type POSIXQueue struct{}

func CreatePOSIXQueue(string, int) (*POSIXQueue, error) { return nil, ErrPOSIXQueueUnsupported }
func OpenPOSIXQueue(string, int) (*POSIXQueue, error)   { return nil, ErrPOSIXQueueUnsupported }
func (*POSIXQueue) Name() string                        { return "" }
func (*POSIXQueue) Identity() POSIXQueueIdentity        { return POSIXQueueIdentity{} }
func (*POSIXQueue) ReadMessage(context.Context, []byte) (int, uint32, error) {
	return 0, 0, ErrPOSIXQueueUnsupported
}
func (*POSIXQueue) WriteMessage(context.Context, []byte) error { return ErrPOSIXQueueUnsupported }
func (*POSIXQueue) Close() error                               { return ErrPOSIXQueueUnsupported }
