//go:build !darwin && !linux

package adapter

import (
	"errors"
	"net"
	"os"
)

func OpenSocketPairStream(*os.File) (net.Conn, error) {
	return nil, errors.New("socket-pair ingress is unavailable on this platform")
}

func OpenInheritedUnixStream(*os.File) (net.Conn, error) {
	return nil, errors.New("inherited Unix-stream ingress is unavailable on this platform")
}
