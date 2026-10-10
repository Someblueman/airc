package adapter

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrMappedQueueInvalid reports an invalid slot, object, or private queue setup.
var ErrMappedQueueInvalid = errors.New("invalid mapped queue")

func (q *MappedQueue) Name() string {
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

// ValidMappedFileDirectory accepts the canonical bounded directory address
// used by the mapped_file carrier. Ownership and file identity require native
// checks when its pre-provisioned resources are opened.
func ValidMappedFileDirectory(path string) bool {
	return len(path) > 1 && len(path) <= 4096 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}
