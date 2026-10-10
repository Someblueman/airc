//go:build linux

package adapter

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
)

const (
	l2capBacklog             = 64
	l2capOptionsOption       = 1
	l2capOptionsSize         = 12
	l2capModeOffset          = 6
	l2capModeERTM            = 3
	l2capOMTUOffset          = 0
	l2capBluetoothSecurity   = 4
	l2capBluetoothHigh       = 3
	l2capBluetoothHighOption = "\x03\x00"
)

// ListenL2CAP opens one fixed BR/EDR L2CAP PSM and admits only configured
// Bluetooth addresses. Bluetooth metadata is admission control, not AIRC
// identity.
func ListenL2CAP(address string, allowedPeerAddresses []string) (net.Listener, error) {
	local, err := ParseL2CAPAddr(address)
	if err != nil {
		return nil, fmt.Errorf("parse L2CAP listener address: %w", err)
	}
	options := &config.L2CAP{AllowedPeerAddresses: slices.Clone(allowedPeerAddresses)}
	if err := options.Validate(address); err != nil {
		return nil, fmt.Errorf("validate L2CAP listener options: %w", err)
	}
	peers := make(map[[6]byte]struct{}, len(options.AllowedPeerAddresses))
	for _, text := range options.AllowedPeerAddresses {
		peer, err := config.ParseBluetoothAddress(text)
		if err != nil {
			return nil, fmt.Errorf("parse L2CAP peer address: %w", err)
		}
		peers[peer] = struct{}{}
	}

	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.BTPROTO_L2CAP)
	if err != nil {
		return nil, fmt.Errorf("create L2CAP listener: %w", err)
	}
	file := os.NewFile(uintptr(fd), "l2cap-listener")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap L2CAP listener")
	}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	if err := requireL2CAPSecurityHigh(fd); err != nil {
		return nil, fmt.Errorf("require Bluetooth high security: %w", err)
	}
	if err := requireL2CAPERTM(fd); err != nil {
		return nil, fmt.Errorf("require L2CAP ERTM: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrL2{PSM: local.PSM, CID: 0, Addr: local.Address, AddrType: 0}); err != nil {
		return nil, fmt.Errorf("bind L2CAP listener: %w", err)
	}
	if err := unix.Listen(fd, l2capBacklog); err != nil {
		return nil, fmt.Errorf("listen on L2CAP endpoint: %w", err)
	}
	boundRaw, err := unix.Getsockname(fd)
	if err != nil {
		return nil, fmt.Errorf("read bound L2CAP address: %w", err)
	}
	bound, ok := boundRaw.(*unix.SockaddrL2)
	if !ok || reverseBluetoothAddress(bound.Addr) != local.Address || l2capNative16(bound.PSM) != local.PSM || l2capNative16(bound.CID) != 0 || bound.AddrType != 0 {
		return nil, errors.New("kernel returned an unexpected bound L2CAP address")
	}
	if err := checkL2CAPSecurityHigh(fd, true); err != nil {
		return nil, fmt.Errorf("verify L2CAP listener security: %w", err)
	}
	listenerOptions, err := readL2CAPOptions(fd)
	if err != nil || listenerOptions[l2capModeOffset] != l2capModeERTM {
		return nil, errors.New("L2CAP listener did not retain ERTM mode")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("prepare L2CAP listener poller: %w", err)
	}
	failed = false
	return &l2capListener{file: file, raw: raw, addr: cloneL2CAPAddr(local), peers: peers}, nil
}

func requireL2CAPSecurityHigh(fd int) error {
	if err := unix.SetsockoptString(fd, unix.SOL_BLUETOOTH, l2capBluetoothSecurity, l2capBluetoothHighOption); err != nil {
		return err
	}
	return checkL2CAPSecurityHigh(fd, true)
}

func checkL2CAPSecurityHigh(fd int, exact bool) error {
	level, err := unix.GetsockoptByte(fd, unix.SOL_BLUETOOTH, l2capBluetoothSecurity)
	if err != nil {
		return err
	}
	if (exact && level != l2capBluetoothHigh) || (!exact && level < l2capBluetoothHigh) {
		return errors.New("bluetooth security level is below high")
	}
	return nil
}

func requireL2CAPERTM(fd int) error {
	options, err := readL2CAPOptions(fd)
	if err != nil {
		return err
	}
	options[l2capModeOffset] = l2capModeERTM
	if err := unix.SetsockoptString(fd, unix.SOL_L2CAP, l2capOptionsOption, string(options[:])); err != nil {
		return err
	}
	readback, err := readL2CAPOptions(fd)
	if err != nil {
		return err
	}
	if readback[l2capModeOffset] != l2capModeERTM {
		return errors.New("kernel did not retain L2CAP ERTM mode")
	}
	return nil
}

func readL2CAPOptions(fd int) ([l2capOptionsSize]byte, error) {
	// L2CAP_OPTIONS is a fixed native-endian UAPI structure. String getters
	// cannot read it because they truncate at the first embedded NUL.
	var options [l2capOptionsSize]byte
	length := uint32(len(options))
	_, _, errno := unix.Syscall6(
		unix.SYS_GETSOCKOPT,
		uintptr(fd), uintptr(unix.SOL_L2CAP), uintptr(l2capOptionsOption),
		uintptr(unsafe.Pointer(&options[0])), uintptr(unsafe.Pointer(&length)), 0,
	)
	runtime.KeepAlive(options[:])
	if errno != 0 {
		return [l2capOptionsSize]byte{}, errno
	}
	if length != l2capOptionsSize {
		return [l2capOptionsSize]byte{}, errors.New("kernel returned an invalid L2CAP options length")
	}
	return options, nil
}

func l2capNative16(value uint16) uint16 {
	// x/sys v0.48.0 decodes SockaddrL2 by copying the raw little-endian
	// PSM/CID fields, although its bind path accepts host-order fields.
	var raw [2]byte
	binary.NativeEndian.PutUint16(raw[:], value)
	return binary.LittleEndian.Uint16(raw[:])
}

func l2capOptions16(options [l2capOptionsSize]byte, offset int) uint16 {
	return binary.NativeEndian.Uint16(options[offset : offset+2])
}

type l2capListener struct {
	file      *os.File
	raw       syscall.RawConn
	addr      *L2CAPAddr
	peers     map[[6]byte]struct{}
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (l *l2capListener) Accept() (net.Conn, error) {
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
			return nil, fmt.Errorf("accept L2CAP connection: %w", err)
		}
		if acceptErr != nil {
			if l.closed.Load() || errors.Is(acceptErr, unix.EBADF) || errors.Is(acceptErr, os.ErrClosed) {
				return nil, net.ErrClosed
			}
			return nil, fmt.Errorf("accept L2CAP connection: %w", acceptErr)
		}
		conn, err := newL2CAPConn(acceptedFD, l.addr, l.peers)
		if err != nil {
			if errors.Is(err, errL2CAPPeerRefused) {
				continue
			}
			return nil, err
		}
		return conn, nil
	}
}

var errL2CAPPeerRefused = errors.New("L2CAP peer is not allowed")

func newL2CAPConn(fd int, listenerAddr *L2CAPAddr, allowed map[[6]byte]struct{}) (*l2capConn, error) {
	file := os.NewFile(uintptr(fd), "l2cap-connection")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap accepted L2CAP connection")
	}
	if err := checkL2CAPSecurityHigh(fd, false); err != nil {
		return nil, errors.Join(errL2CAPPeerRefused, err, file.Close())
	}
	localRaw, err := unix.Getsockname(fd)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read local L2CAP connection address: %w", err), file.Close())
	}
	remoteRaw, err := unix.Getpeername(fd)
	if err != nil {
		return nil, errors.Join(errL2CAPPeerRefused, fmt.Errorf("read remote L2CAP peer address: %w", err), file.Close())
	}
	local, localOK := localRaw.(*unix.SockaddrL2)
	remote, remoteOK := remoteRaw.(*unix.SockaddrL2)
	if !localOK || reverseBluetoothAddress(local.Addr) != listenerAddr.Address || l2capNative16(local.PSM) != listenerAddr.PSM || local.AddrType != 0 || l2capNative16(local.CID) < 0x40 {
		return nil, errors.Join(errors.New("kernel returned an invalid local L2CAP connection address"), file.Close())
	}
	if !remoteOK || remote.AddrType != 0 || l2capNative16(remote.PSM) != listenerAddr.PSM || l2capNative16(remote.CID) < 0x40 {
		return nil, errors.Join(errL2CAPPeerRefused, errors.New("peer returned an invalid L2CAP address or CID"), file.Close())
	}
	peerAddress := reverseBluetoothAddress(remote.Addr)
	if _, ok := allowed[peerAddress]; !ok {
		return nil, errors.Join(errL2CAPPeerRefused, file.Close())
	}
	childOptions, err := readL2CAPOptions(fd)
	if err != nil {
		return nil, errors.Join(errL2CAPPeerRefused, err, file.Close())
	}
	if childOptions[l2capModeOffset] != l2capModeERTM {
		return nil, errors.Join(errL2CAPPeerRefused, errors.New("peer did not negotiate L2CAP ERTM"), file.Close())
	}
	omtu := l2capOptions16(childOptions, l2capOMTUOffset)
	if omtu < 48 {
		return nil, errors.Join(errL2CAPPeerRefused, errors.New("peer negotiated an invalid L2CAP MTU"), file.Close())
	}
	return &l2capConn{
		file: file, local: cloneL2CAPAddr(listenerAddr),
		remote: &L2CAPAddr{Address: peerAddress, PSM: listenerAddr.PSM}, omtu: int(omtu),
	}, nil
}

func (l *l2capListener) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}

func (l *l2capListener) Addr() net.Addr {
	if l == nil {
		return (*L2CAPAddr)(nil)
	}
	return cloneL2CAPAddr(l.addr)
}

type l2capConn struct {
	file      *os.File
	local     *L2CAPAddr
	remote    *L2CAPAddr
	omtu      int
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (c *l2capConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil || c.file == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	n, err := c.file.Read(p)
	return n, c.wrapIOError("read", normalizeL2CAPError(err, c.closed.Load()))
}

func (c *l2capConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c == nil || c.file == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for written < len(p) {
		end := min(written+c.omtu, len(p))
		for written < end {
			n, err := c.file.Write(p[written:end])
			written += n
			if err != nil {
				return written, c.wrapIOError("write", normalizeL2CAPError(err, c.closed.Load()))
			}
			if n == 0 {
				return written, c.wrapIOError("write", io.ErrNoProgress)
			}
		}
	}
	return written, nil
}

func (c *l2capConn) wrapIOError(operation string, err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	return &net.OpError{Op: operation, Net: "bluetooth_l2cap", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: err}
}

func normalizeL2CAPError(err error, closed bool) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	if closed || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EBADF) {
		return net.ErrClosed
	}
	return err
}

func (c *l2capConn) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.closeErr = c.file.Close()
	})
	return c.closeErr
}

func (c *l2capConn) LocalAddr() net.Addr {
	if c == nil {
		return (*L2CAPAddr)(nil)
	}
	return cloneL2CAPAddr(c.local)
}

func (c *l2capConn) RemoteAddr() net.Addr {
	if c == nil {
		return (*L2CAPAddr)(nil)
	}
	return cloneL2CAPAddr(c.remote)
}

func (c *l2capConn) SetDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetDeadline(deadline))
}

func (c *l2capConn) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetReadDeadline(deadline))
}

func (c *l2capConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.wrapIOError("set", c.file.SetWriteDeadline(deadline))
}

func cloneL2CAPAddr(address *L2CAPAddr) *L2CAPAddr {
	if address == nil {
		return nil
	}
	copy := *address
	return &copy
}

var _ net.Listener = (*l2capListener)(nil)
var _ net.Conn = (*l2capConn)(nil)
