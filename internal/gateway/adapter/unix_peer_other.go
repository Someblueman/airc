//go:build !linux && !darwin

package adapter

import (
	"net"
	"os"
)

func unixPeerAllowed(net.Conn) (int, error) { return 0, ErrUnixUnsupported }
func unixPathOwner(os.FileInfo) bool        { return false }
func unixPlatformSupported() bool           { return false }
func unixTrustedAncestor(os.FileInfo) bool  { return false }
