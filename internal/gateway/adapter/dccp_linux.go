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

	"golang.org/x/sys/unix"
)

const (
	dccpProtocol         = unix.IPPROTO_DCCP
	dccpServiceOption    = 2
	dccpCurrentMPSOption = 5
	dccpSendCSCovOption  = 10
	dccpRecvCSCovOption  = 11
	dccpQueuePolicyID    = 16
	dccpQueuePolicyLen   = 17
	dccpQueueSimple      = 0
	dccpQueueLength      = 64
	dccpServiceCode      = "AIRC"
	dccpRecordLimit      = 1000
	dccpListenBacklog    = 128
	dccpSocketBuffer     = 128 << 10
)

var errDCCPRecordTooLarge = errors.New("dccp record exceeds limit")
var errDCCPPeerRefused = errors.New("dccp peer disconnected during accept")

func ListenDCCP(address string) (net.Listener, error) {
	local, err := ParseDCCPAddr(address)
	if err != nil {
		return nil, fmt.Errorf("parse dccp listener address: %w", err)
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DCCP|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, dccpProtocol)
	if err != nil {
		return nil, fmt.Errorf("create dccp listener: %w", err)
	}
	file := os.NewFile(uintptr(fd), "dccp-listener")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap dccp listener")
	}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	// The service code is a network-order __be32 required by the DCCP peer.
	if err := unix.SetsockoptString(fd, unix.SOL_DCCP, dccpServiceOption, dccpServiceCode); err != nil {
		return nil, fmt.Errorf("set dccp service code: %w", err)
	}
	for _, option := range []struct{ name, value int }{
		{dccpSendCSCovOption, 0},
		{dccpRecvCSCovOption, 0},
		{dccpQueuePolicyID, dccpQueueSimple},
		{dccpQueuePolicyLen, dccpQueueLength},
	} {
		if err := unix.SetsockoptInt(fd, unix.SOL_DCCP, option.name, option.value); err != nil {
			return nil, fmt.Errorf("configure dccp socket option %d: %w", option.name, err)
		}
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, dccpSocketBuffer); err != nil {
		return nil, fmt.Errorf("bound dccp receive queue: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, dccpSocketBuffer); err != nil {
		return nil, fmt.Errorf("bound dccp send queue: %w", err)
	}
	if err := unix.Bind(fd, dccpSockaddr(local)); err != nil {
		return nil, fmt.Errorf("bind dccp listener: %w", err)
	}
	if err := unix.Listen(fd, dccpListenBacklog); err != nil {
		return nil, fmt.Errorf("listen on dccp endpoint: %w", err)
	}
	bound, err := unix.Getsockname(fd)
	if err != nil {
		return nil, fmt.Errorf("read bound dccp address: %w", err)
	}
	boundIP, ok := bound.(*unix.SockaddrInet4)
	if !ok || boundIP.Port != local.Port || !net.IP(boundIP.Addr[:]).Equal(local.IP) {
		return nil, errors.New("kernel returned an unexpected bound dccp address")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("prepare dccp listener poller: %w", err)
	}
	failed = false
	return &dccpListener{
		file: file,
		raw:  raw,
		addr: &DCCPAddr{IP: append(net.IP(nil), local.IP...), Port: local.Port},
	}, nil
}

func dccpSockaddr(address *DCCPAddr) *unix.SockaddrInet4 {
	var ip [4]byte
	copy(ip[:], address.IP.To4())
	return &unix.SockaddrInet4{Port: address.Port, Addr: ip}
}

type dccpListener struct {
	file      *os.File
	raw       syscall.RawConn
	addr      *DCCPAddr
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (l *dccpListener) Accept() (net.Conn, error) {
	if l == nil || l.file == nil || l.raw == nil || l.closed.Load() {
		return nil, net.ErrClosed
	}
	for {
		acceptedFD := -1
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
			return nil, &net.OpError{Op: "accept", Net: "dccp", Addr: l.Addr(), Err: err}
		}
		if acceptErr != nil {
			if l.closed.Load() || errors.Is(acceptErr, unix.EBADF) || errors.Is(acceptErr, os.ErrClosed) {
				return nil, net.ErrClosed
			}
			return nil, &net.OpError{Op: "accept", Net: "dccp", Addr: l.Addr(), Err: acceptErr}
		}
		conn, err := newDCCPConn(acceptedFD, l.addr)
		if err != nil {
			if errors.Is(err, errDCCPPeerRefused) {
				continue
			}
			return nil, err
		}
		return conn, nil
	}
}

func newDCCPConn(fd int, listenerAddr *DCCPAddr) (*dccpConn, error) {
	file := os.NewFile(uintptr(fd), "dccp-connection")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap accepted dccp connection")
	}
	localRaw, err := unix.Getsockname(fd)
	if err != nil {
		return nil, errors.Join(errDCCPPeerRefused, fmt.Errorf("read local dccp connection address: %w", err), file.Close())
	}
	remoteRaw, err := unix.Getpeername(fd)
	if err != nil {
		return nil, errors.Join(errDCCPPeerRefused, fmt.Errorf("read remote dccp connection address: %w", err), file.Close())
	}
	localSock, localOK := localRaw.(*unix.SockaddrInet4)
	remoteSock, remoteOK := remoteRaw.(*unix.SockaddrInet4)
	if !localOK {
		return nil, errors.Join(errDCCPPeerRefused, errors.New("kernel returned an invalid dccp connection address"), file.Close())
	}
	if !remoteOK {
		return nil, errors.Join(errDCCPPeerRefused, errors.New("kernel returned an invalid dccp peer address"), file.Close())
	}
	local := &DCCPAddr{IP: append(net.IP(nil), localSock.Addr[:]...), Port: localSock.Port}
	remote := &DCCPAddr{IP: append(net.IP(nil), remoteSock.Addr[:]...), Port: remoteSock.Port}
	if !validDCCPAddr(local) || local.Port != listenerAddr.Port || !local.IP.Equal(listenerAddr.IP) {
		return nil, errors.Join(errDCCPPeerRefused, errors.New("kernel returned an unexpected dccp connection address"), file.Close())
	}
	if !validDCCPAddr(remote) {
		return nil, errors.Join(errDCCPPeerRefused, errors.New("kernel returned an invalid dccp peer address"), file.Close())
	}
	currentMPS, err := dccpCurrentMPS(fd)
	if err != nil {
		return nil, errors.Join(errDCCPPeerRefused, fmt.Errorf("read dccp maximum packet size: %w", err), file.Close())
	}
	if currentMPS < 1 {
		return nil, errors.Join(errDCCPPeerRefused, errors.New("kernel reported an invalid dccp maximum packet size"), file.Close())
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("prepare dccp connection poller: %w", err), file.Close())
	}
	return &dccpConn{file: file, raw: raw, local: local, remote: remote}, nil
}

func dccpCurrentMPS(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_DCCP, dccpCurrentMPSOption)
}

func (l *dccpListener) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}

func (l *dccpListener) Addr() net.Addr {
	if l == nil || l.addr == nil {
		return (*DCCPAddr)(nil)
	}
	return &DCCPAddr{IP: append(net.IP(nil), l.addr.IP...), Port: l.addr.Port}
}

type dccpConn struct {
	file      *os.File
	raw       syscall.RawConn
	local     *DCCPAddr
	remote    *DCCPAddr
	readMu    sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (c *dccpConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil {
		return 0, net.ErrClosed
	}
	n, _, err := c.ReadFrom(p)
	return n, err
}

func (c *dccpConn) readRecord() ([]byte, error) {
	if c == nil || c.file == nil || c.raw == nil || c.closed.Load() {
		return nil, net.ErrClosed
	}
	var buffer [dccpRecordLimit + 1]byte
	var n, oobn, flags int
	var receiveErr error
	err := c.raw.Read(func(fd uintptr) bool {
		for {
			n, oobn, flags, _, receiveErr = unix.Recvmsg(int(fd), buffer[:], nil, unix.MSG_DONTWAIT|unix.MSG_TRUNC)
			if errors.Is(receiveErr, unix.EINTR) {
				continue
			}
			if errors.Is(receiveErr, unix.EAGAIN) || errors.Is(receiveErr, unix.EWOULDBLOCK) {
				return false
			}
			return true
		}
	})
	if err != nil {
		return nil, c.wrapError("read", err)
	}
	if receiveErr != nil {
		if c.closed.Load() || errors.Is(receiveErr, unix.EBADF) || errors.Is(receiveErr, os.ErrClosed) {
			return nil, net.ErrClosed
		}
		return nil, c.wrapError("read", receiveErr)
	}
	if n == 0 {
		_ = c.Close()
		return nil, io.EOF
	}
	if oobn != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n > dccpRecordLimit {
		_ = c.Close()
		return nil, errDCCPRecordTooLarge
	}
	return append([]byte(nil), buffer[:n]...), nil
}

func (c *dccpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c == nil {
		return 0, nil, net.ErrClosed
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	record, err := c.readRecord()
	if err != nil {
		return 0, nil, err
	}
	if len(p) < len(record) {
		_ = c.Close()
		return 0, nil, errors.New("dccp read buffer is too small for complete record")
	}
	return copy(p, record), c.RemoteAddr(), nil
}

func (c *dccpConn) Write(p []byte) (int, error) {
	if c == nil || c.file == nil || c.raw == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	if len(p) == 0 || len(p) > dccpRecordLimit {
		return 0, errDCCPRecordTooLarge
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var mps int
	var mpsErr error
	if err := c.raw.Control(func(fd uintptr) { mps, mpsErr = dccpCurrentMPS(int(fd)) }); err != nil {
		return 0, c.wrapError("write", err)
	}
	if mpsErr != nil {
		return 0, c.wrapError("write", mpsErr)
	}
	if len(p) > mps {
		return 0, errDCCPRecordTooLarge
	}
	var n int
	var sendErr error
	err := c.raw.Write(func(fd uintptr) bool {
		for {
			n, sendErr = unix.SendmsgN(int(fd), p, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
			if errors.Is(sendErr, unix.EINTR) {
				continue
			}
			if errors.Is(sendErr, unix.EAGAIN) || errors.Is(sendErr, unix.EWOULDBLOCK) {
				return false
			}
			return true
		}
	})
	if err != nil {
		return 0, c.wrapError("write", err)
	}
	if sendErr != nil {
		return 0, c.wrapError("write", sendErr)
	}
	if n != len(p) {
		_ = c.Close()
		return n, io.ErrShortWrite
	}
	return n, nil
}

func (c *dccpConn) WriteTo(p []byte, address net.Addr) (int, error) {
	if c == nil {
		return 0, net.ErrClosed
	}
	peer, ok := address.(*DCCPAddr)
	if !ok || !validDCCPAddr(peer) || peer.Port != c.remote.Port || !peer.IP.Equal(c.remote.IP) {
		return 0, errors.New("dccp destination does not match connected peer")
	}
	return c.Write(p)
}

func (c *dccpConn) wrapError(op string, err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return err
	}
	if c.closed.Load() || errors.Is(err, os.ErrClosed) || errors.Is(err, unix.EBADF) {
		return net.ErrClosed
	}
	return &net.OpError{Op: op, Net: "dccp", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: err}
}

func (c *dccpConn) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.closeErr = c.file.Close()
	})
	return c.closeErr
}

func (c *dccpConn) LocalAddr() net.Addr {
	if c == nil || c.local == nil {
		return (*DCCPAddr)(nil)
	}
	return &DCCPAddr{IP: append(net.IP(nil), c.local.IP...), Port: c.local.Port}
}

func (c *dccpConn) RemoteAddr() net.Addr {
	if c == nil || c.remote == nil {
		return (*DCCPAddr)(nil)
	}
	return &DCCPAddr{IP: append(net.IP(nil), c.remote.IP...), Port: c.remote.Port}
}

func (c *dccpConn) SetDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapError("set", c.file.SetDeadline(deadline))
}

func (c *dccpConn) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapError("set", c.file.SetReadDeadline(deadline))
}

func (c *dccpConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapError("set", c.file.SetWriteDeadline(deadline))
}

var _ net.Listener = (*dccpListener)(nil)
var _ net.Conn = (*dccpConn)(nil)
var _ net.PacketConn = (*dccpConn)(nil)
