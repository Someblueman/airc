//go:build darwin || linux

package adapter

import (
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// OpenSocketPairStream owns a pollable duplicate of a dedicated inherited
// endpoint. Both endpoints must be unnamed Unix stream sockets. Descriptor
// ownership provides a local channel boundary; each frame still requires
// authentication. The source descriptor remains the caller's responsibility.
func OpenSocketPairStream(source *os.File) (net.Conn, error) {
	return openInheritedUnixStream(source, true)
}

// OpenInheritedUnixStream accepts an already connected Unix stream endpoint
// passed by the parent, including named connections. It owns only its pollable
// duplicate; source ownership and application authentication remain separate.
func OpenInheritedUnixStream(source *os.File) (net.Conn, error) {
	return openInheritedUnixStream(source, false)
}

func openInheritedUnixStream(source *os.File, unnamed bool) (net.Conn, error) {
	if source == nil {
		return nil, errors.New("missing Unix-stream descriptor")
	}
	raw, err := source.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("inspect inherited Unix stream: %w", err)
	}
	var inspection error
	err = raw.Control(func(fd uintptr) {
		kind, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE)
		if e != nil {
			inspection = e
			return
		}
		if kind != unix.SOCK_STREAM {
			inspection = errors.New("inherited Unix stream requires stream sockets")
			return
		}
		local, e := unix.Getsockname(int(fd))
		if e != nil {
			inspection = e
			return
		}
		peer, e := unix.Getpeername(int(fd))
		if e != nil {
			inspection = e
			return
		}
		l, localUnix := local.(*unix.SockaddrUnix)
		p, peerUnix := peer.(*unix.SockaddrUnix)
		if !localUnix || !peerUnix {
			inspection = errors.New("inherited stream requires connected Unix endpoints")
			return
		}
		if unnamed && (l.Name != "" || p.Name != "") {
			inspection = errors.New("socket pair requires connected unnamed Unix endpoints")
		}
	})
	if err != nil || inspection != nil {
		return nil, fmt.Errorf("validate inherited Unix stream: %w", errors.Join(err, inspection))
	}
	// FileConn duplicates with close-on-exec and attaches the Go network poller.
	// The parent's endpoint must be dedicated because nonblocking status is shared.
	conn, err := net.FileConn(source)
	if err != nil {
		return nil, fmt.Errorf("duplicate inherited Unix stream: %w", err)
	}
	return conn, nil
}
