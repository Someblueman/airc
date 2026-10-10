package machipc

import (
	"errors"
	"os"
)

const (
	MaxRequestBytes  = 16 << 10
	MaxResponseBytes = 64 << 10
	RequestID        = 0x41495201
	ResponseID       = 0x41495202
)

var (
	ErrInvalid     = errors.New("invalid Mach IPC request")
	ErrClosed      = errors.New("mach IPC port is closed")
	ErrUnavailable = errors.New("mach IPC is unavailable on this target")
	ErrNameInUse   = errors.Join(os.ErrExist, errors.New("mach IPC bootstrap name is already registered"))
	ErrReplied     = errors.New("mach IPC reply right has already been used")
)

// ValidName accepts a single bootstrap service name without a namespace path.
func ValidName(name string) bool {
	if len(name) == 0 || len(name) > 127 {
		return false
	}
	for i, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}
