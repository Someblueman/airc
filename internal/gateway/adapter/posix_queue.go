package adapter

import (
	"errors"
	"strings"
)

const POSIXQueueMessageBytes = 1000
const POSIXQueueMaxDepth = 10

var ErrPOSIXQueueUnsupported = errors.New("POSIX queues require Linux ARM64 or AMD64")
var ErrPOSIXQueueInvalid = errors.New("invalid POSIX queue name, ownership or attributes")

// POSIXQueueIdentity identifies the live kernel inode, not an authenticated sender.
type POSIXQueueIdentity struct{ Device, Inode uint64 }

func ValidPOSIXQueueName(name string) bool {
	if len(name) < 2 || len(name) > 255 || name[0] != '/' {
		return false
	}
	for i, c := range name[1:] {
		alphanumeric := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alphanumeric && (i == 0 || !strings.ContainsRune("-_.", c)) {
			return false
		}
	}
	return true
}
