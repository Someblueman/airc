package sysvqueue

import "errors"

const MessageBytes = 1000

var (
	ErrInvalid     = errors.New("invalid system V queue")
	ErrUnsupported = errors.New("system V queue unsupported on this platform")
)

// Identity is kernel lifecycle metadata, never producer authentication.
type Identity struct {
	ID       int
	Key      int32
	Sequence uint16
	Changed  int64
}
