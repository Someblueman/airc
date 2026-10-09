package adapter

import (
	"errors"
	"os"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/sharedmem"
)

const (
	sharedMemoryBytes  = sharedmem.MaxMappingBytes
	sharedSlotHeader   = 16
	sharedSlotBody     = 1000
	sharedStateEmpty   = uint32(0)
	sharedStateWriting = uint32(1)
	sharedStateReady   = uint32(2)
)

// ErrSharedMemoryInvalid reports an invalid slot, object, or private queue setup.
var ErrSharedMemoryInvalid = errors.New("invalid shared-memory queue")

var (
	errSharedMemoryQueueBuffer = errors.New("shared-memory read buffer too small")
	errSharedMemoryQueueSide   = errors.New("shared-memory queue operation is not allowed on this side")
)

// SharedMemoryQueue moves one whole AGD1 fragment through an externally
// provisioned, single-slot POSIX shared-memory object. The object and lock files
// must not be resized, unlinked, or replaced until every handle has closed;
// same-UID peers must cooperate, especially on Darwin where shared-memory name
// replacement cannot be checked by object identity. This is process-restart
// state, not reboot-durable storage. Close wakes waiters and serializes unmapping
// against active mapped access; callers that expose operations concurrently
// must join them before considering shutdown complete.
type SharedMemoryQueue struct {
	name      string
	directory string
	consumer  bool

	mu         sync.Mutex
	operations chan struct{}
	closed     chan struct{}
	closeOnce  sync.Once
	closeErr   error

	root      *os.Root
	dirAnchor *os.File
	dirFD     int
	lockFile  *os.File
	lockFD    int
	mapping   *sharedmem.Mapping
	view      []byte
}

func (q *SharedMemoryQueue) Name() string {
	if q == nil {
		return ""
	}
	return q.name
}

// ValidSharedMemoryName accepts a slash and up to 30 ASCII component bytes:
// first alphanumeric, then alphanumeric, underscore, dot, or hyphen.
func ValidSharedMemoryName(name string) bool {
	if len(name) < 2 || len(name) > 31 || name[0] != '/' {
		return false
	}
	isAlphaNum := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	if !isAlphaNum(name[1]) {
		return false
	}
	for i := 2; i < len(name); i++ {
		if !isAlphaNum(name[i]) && name[i] != '_' && name[i] != '.' && name[i] != '-' {
			return false
		}
	}
	return true
}
