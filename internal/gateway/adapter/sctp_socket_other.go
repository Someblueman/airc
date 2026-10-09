//go:build !linux || !arm64

package adapter

import (
	"errors"
	"net"
)

// ListenSCTP is available only on Linux ARM64.
func ListenSCTP(string) (net.Listener, error) {
	return nil, errors.New("sctp listener requires linux arm64")
}
