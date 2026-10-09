//go:build !((darwin && arm64) || (linux && arm64))

package adapter

import (
	"context"
	"github.com/Someblueman/airc/internal/gateway/sharedmem"
)

func OpenSharedMemoryQueue(name, directory string, consumer bool) (*SharedMemoryQueue, error) {
	return nil, sharedmem.ErrUnsupported
}
func (q *SharedMemoryQueue) ReadMessage(context.Context, []byte) (int, error) {
	return 0, sharedmem.ErrUnsupported
}
func (q *SharedMemoryQueue) WriteMessage(context.Context, []byte) error {
	return sharedmem.ErrUnsupported
}
func (q *SharedMemoryQueue) Close() error { return nil }
