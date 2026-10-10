//go:build linux

package adapter

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
)

const (
	rfcommBacklog             = 64
	bluetoothSecurityOption   = 4
	bluetoothSecurityHigh     = 3
	bluetoothSecurityHighData = "\x03\x00"
)

// ListenRFCOMM creates a fixed local RFCOMM endpoint and admits only the
// configured peer addresses. Bluetooth address metadata is admission control,
// not an AIRC identity.
func ListenRFCOMM(address string, allowedPeerAddresses []string) (net.Listener, error) {
	local, err := ParseRFCOMMAddr(address)
	if err != nil {
		return nil, fmt.Errorf("parse RFCOMM listener address: %w", err)
	}
	options := &config.RFCOMM{AllowedPeerAddresses: slices.Clone(allowedPeerAddresses)}
	if err := options.Validate(address); err != nil {
		return nil, fmt.Errorf("validate RFCOMM listener options: %w", err)
	}
	peers := make(map[[6]byte]struct{}, len(options.AllowedPeerAddresses))
	for _, text := range options.AllowedPeerAddresses {
		peer, err := config.ParseBluetoothAddress(text)
		if err != nil {
			return nil, fmt.Errorf("parse RFCOMM peer address: %w", err)
		}
		peers[peer] = struct{}{}
	}

	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.BTPROTO_RFCOMM)
	if err != nil {
		return nil, fmt.Errorf("create RFCOMM listener: %w", err)
	}
	if err := requireRFCOMMSecurityHigh(fd); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("require Bluetooth high security: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrRFCOMM{Addr: reverseBluetoothAddress(local.Address), Channel: local.Channel}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind RFCOMM listener: %w", err)
	}
	if err := unix.Listen(fd, rfcommBacklog); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("listen on RFCOMM endpoint: %w", err)
	}
	bound, err := unix.Getsockname(fd)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("read bound RFCOMM address: %w", err)
	}
	boundRFCOMM, ok := bound.(*unix.SockaddrRFCOMM)
	if !ok || reverseBluetoothAddress(boundRFCOMM.Addr) != local.Address || boundRFCOMM.Channel != local.Channel {
		_ = unix.Close(fd)
		return nil, errors.New("kernel returned an unexpected bound RFCOMM address")
	}
	security, err := unix.GetsockoptByte(fd, unix.SOL_BLUETOOTH, bluetoothSecurityOption)
	if err != nil || security != bluetoothSecurityHigh {
		_ = unix.Close(fd)
		return nil, errors.New("RFCOMM listener lost Bluetooth high security")
	}
	file := os.NewFile(uintptr(fd), "rfcomm-listener")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap RFCOMM listener")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("prepare RFCOMM listener poller: %w", err)
	}
	return &rfcommListener{file: file, raw: raw, addr: cloneRFCOMMAddr(local), peers: peers}, nil
}

func requireRFCOMMSecurityHigh(fd int) error {
	if err := unix.SetsockoptString(fd, unix.SOL_BLUETOOTH, bluetoothSecurityOption, bluetoothSecurityHighData); err != nil {
		return err
	}
	return checkRFCOMMSecurityHigh(fd)
}

func checkRFCOMMSecurityHigh(fd int) error {
	level, err := unix.GetsockoptByte(fd, unix.SOL_BLUETOOTH, bluetoothSecurityOption)
	if err != nil {
		return err
	}
	if level != bluetoothSecurityHigh {
		return errors.New("Bluetooth security level is below high")
	}
	return nil
}

func reverseBluetoothAddress(address [6]byte) [6]byte {
	return [6]byte{address[5], address[4], address[3], address[2], address[1], address[0]}
}

type rfcommListener struct {
	file      *os.File
	raw       syscall.RawConn
	addr      *RFCOMMAddr
	peers     map[[6]byte]struct{}
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (l *rfcommListener) Accept() (net.Conn, error) {
	if l == nil || l.file == nil || l.raw == nil || l.closed.Load() {
		return nil, net.ErrClosed
	}
	for {
		acceptedFD := -1
		var acceptErr error
		err := l.raw.Read(func(fd uintptr) bool {
			for {
				child, _, err := unix.Accept4(int(fd), unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC)
				if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ECONNABORTED) {
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
			return nil, fmt.Errorf("accept RFCOMM connection: %w", err)
		}
		if acceptErr != nil {
			if l.closed.Load() || errors.Is(acceptErr, unix.EBADF) || errors.Is(acceptErr, os.ErrClosed) {
				return nil, net.ErrClosed
			}
			return nil, fmt.Errorf("accept RFCOMM connection: %w", acceptErr)
		}
		conn, err := newRFCOMMConn(acceptedFD, l.addr, l.peers)
		if err != nil {
			if errors.Is(err, errRFCOMMPeerRefused) {
				continue
			}
			return nil, err
		}
		return conn, nil
	}
}

var errRFCOMMPeerRefused = errors.New("RFCOMM peer is not allowed")

func newRFCOMMConn(fd int, listenerAddr *RFCOMMAddr, allowed map[[6]byte]struct{}) (*rfcommConn, error) {
	file := os.NewFile(uintptr(fd), "rfcomm-connection")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap accepted RFCOMM connection")
	}
	if err := checkRFCOMMSecurityHigh(fd); err != nil {
		return nil, errors.Join(errRFCOMMPeerRefused, err, file.Close())
	}
	localRaw, err := unix.Getsockname(fd)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read local RFCOMM connection address: %w", err), file.Close())
	}
	remoteRaw, err := unix.Getpeername(fd)
	if err != nil {
		return nil, errors.Join(errRFCOMMPeerRefused, fmt.Errorf("read remote RFCOMM peer address: %w", err), file.Close())
	}
	local, localOK := localRaw.(*unix.SockaddrRFCOMM)
	remote, remoteOK := remoteRaw.(*unix.SockaddrRFCOMM)
	if !localOK || reverseBluetoothAddress(local.Addr) != listenerAddr.Address || local.Channel != listenerAddr.Channel {
		return nil, errors.Join(errors.New("kernel returned an invalid local RFCOMM connection address"), file.Close())
	}
	if !remoteOK || remote.Channel != listenerAddr.Channel {
		return nil, errors.Join(errRFCOMMPeerRefused, errors.New("peer returned an invalid RFCOMM address or channel"), file.Close())
	}
	peerAddress := reverseBluetoothAddress(remote.Addr)
	if _, ok := allowed[peerAddress]; !ok {
		return nil, errors.Join(errRFCOMMPeerRefused, file.Close())
	}
	return &rfcommConn{
		file: file, local: cloneRFCOMMAddr(listenerAddr),
		remote: &RFCOMMAddr{Address: peerAddress, Channel: remote.Channel},
	}, nil
}

func (l *rfcommListener) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}

func (l *rfcommListener) Addr() net.Addr {
	if l == nil {
		return (*RFCOMMAddr)(nil)
	}
	return cloneRFCOMMAddr(l.addr)
}

type rfcommConn struct {
	file      *os.File
	local     *RFCOMMAddr
	remote    *RFCOMMAddr
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (c *rfcommConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil || c.file == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	n, err := c.file.Read(p)
	return n, c.wrapIOError("read", normalizeRFCOMMError(err, c.closed.Load()))
}

func (c *rfcommConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil || c.file == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	n, err := c.file.Write(p)
	return n, c.wrapIOError("write", normalizeRFCOMMError(err, c.closed.Load()))
}

func (c *rfcommConn) wrapIOError(operation string, err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	return &net.OpError{Op: operation, Net: "bluetooth_rfcomm", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: err}
}

func normalizeRFCOMMError(err error, closed bool) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	if closed || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EBADF) {
		return net.ErrClosed
	}
	return err
}

func (c *rfcommConn) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.closeErr = c.file.Close()
	})
	return c.closeErr
}

func (c *rfcommConn) LocalAddr() net.Addr {
	if c == nil {
		return (*RFCOMMAddr)(nil)
	}
	return cloneRFCOMMAddr(c.local)
}

func (c *rfcommConn) RemoteAddr() net.Addr {
	if c == nil {
		return (*RFCOMMAddr)(nil)
	}
	return cloneRFCOMMAddr(c.remote)
}

func (c *rfcommConn) SetDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetDeadline(deadline))
}

func (c *rfcommConn) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetReadDeadline(deadline))
}

func (c *rfcommConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetWriteDeadline(deadline))
}

func cloneRFCOMMAddr(address *RFCOMMAddr) *RFCOMMAddr {
	if address == nil {
		return nil
	}
	copy := *address
	return &copy
}

var _ net.Listener = (*rfcommListener)(nil)
var _ net.Conn = (*rfcommConn)(nil)
