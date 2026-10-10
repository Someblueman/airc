package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"
)

// Unix datagram sockets may report definite zero-byte buffer pressure rather
// than wait for a reader. Retry that unsent packet within the caller's whole
// response deadline; never retry partial or uncertain writes.
func writePacketFragment(ctx context.Context, conn net.PacketConn, packet []byte, destination net.Addr) error {
	_, unixgram := destination.(*net.UnixAddr)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := conn.WriteTo(packet, destination)
		if err == nil {
			if n != len(packet) {
				return io.ErrShortWrite
			}
			return nil
		}
		if !unixgram || n != 0 || !(errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)) {
			return err
		}
		delay := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			delay.Stop()
			return ctx.Err()
		case <-delay.C:
		}
	}
}
