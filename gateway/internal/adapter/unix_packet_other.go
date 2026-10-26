//go:build !linux

package adapter

import (
	"context"
	"net"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

func ListenUnixPacket(string) (net.Listener, error) { return nil, ErrUnixPacketUnsupported }

func ServeUnixPacket(context.Context, net.Listener, *dispatch.Dispatcher, chan struct{}) error {
	return ErrUnixPacketUnsupported
}
