package machipc

import (
	"errors"
	"os"
	"sync"
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

// Request owns one received send-once reply right. Body is a private copy of
// the inline request bytes and remains valid after the listener reuses its buffer.
type Request struct {
	Body []byte
	UID  uint32
	PID  int32

	mu        sync.Mutex
	replyPort uint32
	replied   bool
}
