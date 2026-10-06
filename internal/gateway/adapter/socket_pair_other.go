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
