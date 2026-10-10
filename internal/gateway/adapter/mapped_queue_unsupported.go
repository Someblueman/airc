//go:build !((darwin && arm64) || (linux && arm64))

package adapter

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/sharedmem"
)

// MappedQueue has no native queue state on unsupported targets.
type MappedQueue struct {
	name       string
	consumer   bool
	fileBacked bool
}

func OpenSharedMemoryQueue(name, directory string, consumer bool) (*MappedQueue, error) {
	return nil, sharedmem.ErrUnsupported
}
func OpenMappedFileQueue(directory string, consumer bool) (*MappedQueue, error) {
	return nil, sharedmem.ErrUnsupported
}
func (q *MappedQueue) ReadMessage(context.Context, []byte) (int, error) {
	return 0, sharedmem.ErrUnsupported
}
func (q *MappedQueue) WriteMessage(context.Context, []byte) error {
	return sharedmem.ErrUnsupported
}
func (q *MappedQueue) Close() error { return nil }
