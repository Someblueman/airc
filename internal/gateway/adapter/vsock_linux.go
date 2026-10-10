//go:build linux

package adapter

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
)

const vsockBacklog = 128

// ListenVSock opens a bounded AF_VSOCK stream listener. Peer CID filtering is
// admission control only; CID metadata is not an authentication identity.
func ListenVSock(address string, allowedPeerCIDs []uint32) (net.Listener, error) {
	local, err := ParseVSockAddr(address)
	if err != nil {
		return nil, fmt.Errorf("parse vsock listener address: %w", err)
	}
	options := &config.VSock{AllowedPeerCIDs: append([]uint32(nil), allowedPeerCIDs...)}
	if err := options.Validate(address); err != nil {
		return nil, fmt.Errorf("validate vsock listener options: %w", err)
	}
	allowed := make(map[uint32]struct{}, len(allowedPeerCIDs))
	for _, cid := range options.AllowedPeerCIDs {
		allowed[cid] = struct{}{}
	}

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("create vsock listener: %w", err)
	}
	file := os.NewFile(uintptr(fd), "vsock-listener")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap vsock listener")
	}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: local.CID, Port: local.Port}); err != nil {
		return nil, fmt.Errorf("bind vsock listener: %w", err)
	}
	if err := unix.Listen(fd, vsockBacklog); err != nil {
		return nil, fmt.Errorf("listen on vsock endpoint: %w", err)
	}
	bound, err := unix.Getsockname(fd)
	if err != nil {
		return nil, fmt.Errorf("read bound vsock address: %w", err)
	}
	boundVM, ok := bound.(*unix.SockaddrVM)
	if !ok || boundVM.Flags != 0 || boundVM.CID != local.CID || boundVM.Port != local.Port {
		return nil, errors.New("kernel returned an unexpected bound vsock address")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("prepare vsock listener poller: %w", err)
	}
	failed = false
	return &vsockListener{
		file:  file,
		raw:   raw,
		addr:  &VSockAddr{CID: boundVM.CID, Port: boundVM.Port},
		peers: allowed,
	}, nil
}

type vsockListener struct {
	file      *os.File
	raw       syscall.RawConn
	addr      *VSockAddr
	peers     map[uint32]struct{}
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (l *vsockListener) Accept() (net.Conn, error) {
	if l == nil || l.file == nil || l.raw == nil || l.closed.Load() {
		return nil, net.ErrClosed
	}
	for {
		var acceptedFD = -1
		var acceptErr error
		err := l.raw.Read(func(fd uintptr) bool {
			for {
				child, _, err := unix.Accept4(int(fd), unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC)
				if errors.Is(err, unix.EINTR) {
					continue
				}
				if errors.Is(err, unix.ECONNABORTED) {
					continue
				}
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
					return false
				}
				if err != nil {
					acceptErr = err
					return true
				}
				acceptedFD = child
				return true
			}
		})
		if err != nil {
			if l.closed.Load() || errors.Is(err, os.ErrClosed) {
				return nil, net.ErrClosed
			}
			return nil, fmt.Errorf("accept vsock connection: %w", err)
		}
		if acceptErr != nil {
			if l.closed.Load() || errors.Is(acceptErr, unix.EBADF) || errors.Is(acceptErr, os.ErrClosed) {
				return nil, net.ErrClosed
			}
			return nil, fmt.Errorf("accept vsock connection: %w", acceptErr)
		}

		conn, err := newVSockConn(acceptedFD, l.addr, l.peers)
		if err != nil {
			if errors.Is(err, errVSockPeerRefused) {
				continue
			}
			return nil, err
		}
		return conn, nil
	}
}

var errVSockPeerRefused = errors.New("vsock peer is not allowed")

func newVSockConn(fd int, listenerAddr *VSockAddr, allowed map[uint32]struct{}) (*vsockConn, error) {
	file := os.NewFile(uintptr(fd), "vsock-connection")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap accepted vsock connection")
	}
	localRaw, err := unix.Getsockname(fd)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read local vsock connection address: %w", err), file.Close())
	}
	remoteRaw, err := unix.Getpeername(fd)
	if err != nil {
		return nil, errors.Join(errVSockPeerRefused, fmt.Errorf("read remote vsock connection address: %w", err), file.Close())
	}
	localVM, localOK := localRaw.(*unix.SockaddrVM)
	remoteVM, remoteOK := remoteRaw.(*unix.SockaddrVM)
	if !localOK || localVM.Flags != 0 || localVM.CID != listenerAddr.CID || localVM.Port != listenerAddr.Port {
		return nil, errors.Join(errors.New("kernel returned an invalid accepted vsock address"), file.Close())
	}
	if !remoteOK || remoteVM.Flags != 0 || !validVSockCID(remoteVM.CID) || !validVSockPort(remoteVM.Port) {
		return nil, errors.Join(errVSockPeerRefused, errors.New("peer returned an invalid vsock address"), file.Close())
	}
	if _, ok := allowed[remoteVM.CID]; !ok {
		return nil, errors.Join(errVSockPeerRefused, file.Close())
	}
	return &vsockConn{
		file:   file,
		local:  &VSockAddr{CID: localVM.CID, Port: localVM.Port},
		remote: &VSockAddr{CID: remoteVM.CID, Port: remoteVM.Port},
	}, nil
}

func (l *vsockListener) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}

func (l *vsockListener) Addr() net.Addr {
	if l == nil || l.addr == nil {
		return (*VSockAddr)(nil)
	}
	return &VSockAddr{CID: l.addr.CID, Port: l.addr.Port}
}

type vsockConn struct {
	file      *os.File
	local     *VSockAddr
	remote    *VSockAddr
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (c *vsockConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil || c.file == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	n, err := c.file.Read(p)
	return n, c.wrapIOError("read", normalizeVSockIOError(err, c.closed.Load()))
}

func (c *vsockConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil || c.file == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	n, err := c.file.Write(p)
	return n, c.wrapIOError("write", normalizeVSockIOError(err, c.closed.Load()))
}

func (c *vsockConn) wrapIOError(op string, err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	return &net.OpError{Op: op, Net: "virtio_vsock", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: err}
}

func normalizeVSockIOError(err error, closed bool) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	if closed || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EBADF) {
		return net.ErrClosed
	}
	return err
}

func (c *vsockConn) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.closeErr = c.file.Close()
	})
	return c.closeErr
}

func (c *vsockConn) LocalAddr() net.Addr {
	if c == nil || c.local == nil {
		return (*VSockAddr)(nil)
	}
	return &VSockAddr{CID: c.local.CID, Port: c.local.Port}
}

func (c *vsockConn) RemoteAddr() net.Addr {
	if c == nil || c.remote == nil {
		return (*VSockAddr)(nil)
	}
	return &VSockAddr{CID: c.remote.CID, Port: c.remote.Port}
}

func (c *vsockConn) SetDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetDeadline(deadline))
}

func (c *vsockConn) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetReadDeadline(deadline))
}

func (c *vsockConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetWriteDeadline(deadline))
}

var _ net.Listener = (*vsockListener)(nil)
var _ net.Conn = (*vsockConn)(nil)
