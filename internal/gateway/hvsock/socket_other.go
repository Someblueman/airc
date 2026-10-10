//go:build !windows

package hvsock

import (
	"context"
	"net"
)

func Listen(context.Context, string, string) (net.Listener, error) {
	return nil, errUnsupported
}

func Dial(context.Context, string) (net.Conn, error) {
	return nil, errUnsupported
}
