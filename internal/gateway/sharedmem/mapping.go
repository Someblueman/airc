// Package sharedmem opens externally provisioned POSIX shared-memory mappings.
// The producer owns creation, sizing, and unlinking. Both sides must cooperate:
// neither may resize or unlink the object while the other uses it. Callers own
// the sidecar transaction lock and must join every access before Close. Darwin
// does not expose stable object identity in shm fstat, so same-UID participants
// must also cooperate not to unlink and replace a name while it is in use.
package sharedmem

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	ErrInvalid     = errors.New("invalid shared memory mapping")
	ErrUnsupported = errors.New("native shared memory mapping is unsupported on this platform")
)

const maxNameBytes = 31

// MaxMappingBytes is the fixed maximum shared-memory region supported by this
// primitive.
const MaxMappingBytes = 16 * 1024

// Mapping holds an open descriptor and a shared read/write byte view. Bytes
// returns the mapped slice while open. Callers must synchronize all slice use
// against Close; after Close, Bytes returns nil.
type Mapping struct {
	mu        sync.Mutex
	file      *os.File
	exactSize int
	bytes     []byte
	closeErr  error
}

// OpenExisting opens but never creates or unlinks the POSIX shared-memory
// object named /name. It accepts only bounded single-component names and
// requires the object to belong to the effective UID, have mode 0600, and have
// exactly exactSize bytes. On Linux, an existing mapping remains attached to
// its opened object after its name is unlinked; that behavior does not provide
// a defense against a producer replacing the name.
func OpenExisting(name string, exactSize int) (*Mapping, error) {
	if !validName(name) || exactSize <= 0 || exactSize > MaxMappingBytes {
		return nil, ErrInvalid
	}
	fd, err := openNative(name)
	if err != nil {
		return nil, fmt.Errorf("open shared memory: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = closeNative(fd)
		return nil, ErrInvalid
	}
	view, err := mapNative(fd, exactSize)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("map shared memory: %w", err)
	}
	return &Mapping{file: file, exactSize: exactSize, bytes: view}, nil
}

func validName(name string) bool {
	if len(name) < 2 || len(name) > maxNameBytes || name[0] != '/' || strings.IndexByte(name[1:], '/') >= 0 {
		return false
	}
	first := name[1]
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z' || first >= '0' && first <= '9') {
		return false
	}
	for _, c := range name[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// Validate rechecks the retained descriptor's owner, mode, size, and native
// object type before a caller begins a bounded transaction. The caller must
// still hold the sidecar lock across validation and access.
func (m *Mapping) Validate() error {
	if m == nil {
		return os.ErrClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return os.ErrClosed
	}
	return checkNative(int(m.file.Fd()), m.exactSize)
}

// Bytes returns the shared mapped region, or nil after Close. The caller must
// prevent concurrent Close until all uses of the returned slice have ended.
func (m *Mapping) Bytes() []byte {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes
}

// Close unmaps and closes the descriptor. It is safe to call repeatedly.
func (m *Mapping) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bytes == nil && m.file == nil {
		return m.closeErr
	}
	var errs []error
	if m.bytes != nil {
		if err := unmapNative(m.bytes); err != nil {
			errs = append(errs, fmt.Errorf("unmap shared memory: %w", err))
		}
		m.bytes = nil
	}
	if m.file != nil {
		if err := m.file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close shared memory: %w", err))
		}
		m.file = nil
	}
	m.closeErr = errors.Join(errs...)
	return m.closeErr
}
