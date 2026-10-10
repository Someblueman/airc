//go:build linux

package adapter

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	canISOTPLevel         = unix.SOL_CAN_BASE + unix.CAN_ISOTP
	canISOTPOptions       = 1
	canISOTPReceiveFC     = 2
	canISOTPLLOptions     = 5
	canISOTPFrameTxtime   = 50_000
	canISOTPMaxPDU        = 1000
	canISOTPReceiveBuffer = 64 << 10
	canISOTPSendBuffer    = 64 << 10
)

type canISOTPNativePackets struct {
	file      *os.File
	raw       syscall.RawConn
	local     *CANISOTPAddr
	ifIndex   int
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
	readMu    sync.Mutex
	writeMu   sync.Mutex
}

func listenCANISOTPNative(address *CANISOTPAddr) (*canISOTPNativePackets, error) {
	if !validCANISOTPAddr(address) {
		return nil, errors.New("invalid CAN ISO-TP listener address")
	}
	iface, err := net.InterfaceByName(address.Interface)
	if err != nil {
		return nil, err
	}
	if iface.Index <= 0 || iface.Flags&net.FlagUp == 0 || !canISOTPInterfaceMTUMatches(address.FD, iface.MTU) {
		return nil, errors.New("CAN ISO-TP interface is down or has an unsupported MTU")
	}

	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.CAN_ISOTP)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "can-isotp")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap CAN ISO-TP socket")
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	if err := verifyCANInterface(fd, address.Interface, iface.Index, iface.MTU, address.FD); err != nil {
		return nil, err
	}
	if err := configureCANISOtpsocket(fd, address.FD); err != nil {
		return nil, err
	}
	if err := setCANISOtpsocketBuffers(fd); err != nil {
		return nil, err
	}
	bound := &unix.SockaddrCAN{Ifindex: iface.Index, RxID: uint32(address.RXID), TxID: uint32(address.TXID)}
	if err := unix.Bind(fd, bound); err != nil {
		return nil, err
	}
	actual, err := unix.Getsockname(fd)
	if err != nil {
		return nil, err
	}
	actualCAN, ok := actual.(*unix.SockaddrCAN)
	if !ok || actualCAN.Ifindex != bound.Ifindex || actualCAN.RxID != bound.RxID || actualCAN.TxID != bound.TxID {
		return nil, errors.New("kernel bound a different CAN ISO-TP endpoint")
	}
	if err := verifyCANInterface(fd, address.Interface, iface.Index, iface.MTU, address.FD); err != nil {
		return nil, err
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}
	if err := file.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return nil, err
	}
	if err := file.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	closeOnError = false
	return &canISOTPNativePackets{file: file, raw: raw, local: cloneCANISOTPAddr(address), ifIndex: iface.Index}, nil
}

// These are the Linux UAPI layouts in linux/can/isotp.h. CAN-FD uses a maximum
// 64-byte transmit data length without bitrate switching.
const (
	canISOTPCANFDMtu = 72
	canISOTPLLOptLen = 3
	canISOTPFCOptLen = 3
	canISOTPOptLen   = 12
)

func configureCANISOtpsocket(fd int, fdMode bool) error {
	var options [canISOTPOptLen]byte
	binary.NativeEndian.PutUint32(options[4:8], canISOTPFrameTxtime)
	if err := unix.SetsockoptString(fd, canISOTPLevel, canISOTPOptions, string(options[:])); err != nil {
		return err
	}
	got, err := getCANISOTPSockopt(fd, canISOTPOptions, len(options))
	if err != nil {
		return err
	}
	if string(got) != string(options[:]) {
		return errors.New("kernel did not retain required CAN ISO-TP options")
	}
	var flowControl = [canISOTPFCOptLen]byte{8, 0, 0}
	if err := unix.SetsockoptString(fd, canISOTPLevel, canISOTPReceiveFC, string(flowControl[:])); err != nil {
		return err
	}
	got, err = getCANISOTPSockopt(fd, canISOTPReceiveFC, len(flowControl))
	if err != nil {
		return err
	}
	if string(got) != string(flowControl[:]) {
		return errors.New("kernel did not retain required CAN ISO-TP flow control")
	}
	linkOptions := [canISOTPLLOptLen]byte{unix.CAN_MTU, 8, 0}
	if fdMode {
		linkOptions = [canISOTPLLOptLen]byte{canISOTPCANFDMtu, 64, 0}
	}
	if err := unix.SetsockoptString(fd, canISOTPLevel, canISOTPLLOptions, string(linkOptions[:])); err != nil {
		return err
	}
	got, err = getCANISOTPSockopt(fd, canISOTPLLOptions, len(linkOptions))
	if err != nil {
		return err
	}
	if string(got) != string(linkOptions[:]) {
		return errors.New("kernel did not retain requested CAN ISO-TP link options")
	}
	return nil
}

func canISOTPInterfaceMTUMatches(fdMode bool, mtu int) bool {
	if fdMode {
		return mtu == canISOTPCANFDMtu
	}
	return mtu == unix.CAN_MTU || mtu == canISOTPCANFDMtu
}

func getCANISOTPSockopt(fd, option, size int) ([]byte, error) {
	value := make([]byte, size)
	length := uint32(size)
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), uintptr(canISOTPLevel), uintptr(option), uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&length)), 0)
	runtime.KeepAlive(value)
	if errno != 0 {
		return nil, errno
	}
	if int(length) != size {
		return nil, errors.New("kernel returned an unexpected CAN ISO-TP option size")
	}
	return value, nil
}

func setCANISOtpsocketBuffers(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, canISOTPReceiveBuffer/2); err != nil {
		return err
	}
	got, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF)
	if err != nil {
		return err
	}
	if got != canISOTPReceiveBuffer {
		return errors.New("kernel CAN ISO-TP receive buffer differs from configured bound")
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, canISOTPSendBuffer/2); err != nil {
		return err
	}
	got, err = unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF)
	if err != nil {
		return err
	}
	if got != canISOTPSendBuffer {
		return errors.New("kernel CAN ISO-TP send buffer differs from configured bound")
	}
	return nil
}

func verifyCANInterface(fd int, name string, index, mtu int, fdMode bool) error {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return err
	}
	if iface.Name != name || iface.Index != index || iface.Flags&net.FlagUp == 0 || iface.MTU != mtu ||
		!canISOTPInterfaceMTUMatches(fdMode, iface.MTU) {
		return errors.New("CAN interface changed while opening ISO-TP socket")
	}
	request, err := unix.NewIfreq(name)
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFHWADDR, request); err != nil {
		return err
	}
	if request.Uint16() != unix.ARPHRD_CAN {
		return errors.New("configured ISO-TP interface is not an ARPHRD_CAN device")
	}
	return nil
}

func (c *canISOTPNativePackets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.file == nil || c.raw == nil || c.closed.Load() {
		return 0, nil, net.ErrClosed
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	var wire [canISOTPMaxPDU + 1]byte
	for {
		var n, oobn, flags int
		var source unix.Sockaddr
		var receiveErr error
		err := c.raw.Read(func(fd uintptr) bool {
			for {
				n, oobn, flags, source, receiveErr = unix.Recvmsg(int(fd), wire[:], nil, unix.MSG_DONTWAIT)
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
			if c.closed.Load() || errors.Is(err, os.ErrClosed) {
				return 0, nil, net.ErrClosed
			}
			return 0, nil, err
		}
		if receiveErr != nil {
			if errors.Is(receiveErr, unix.EINTR) || errors.Is(receiveErr, unix.ETIMEDOUT) ||
				errors.Is(receiveErr, unix.EILSEQ) || errors.Is(receiveErr, unix.EBADMSG) {
				continue
			}
			if c.closed.Load() || errors.Is(receiveErr, unix.EBADF) {
				return 0, nil, net.ErrClosed
			}
			return 0, nil, receiveErr
		}
		if n <= 0 || n > len(wire) || oobn != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n > canISOTPMaxPDU {
			continue
		}
		peer, ok := source.(*unix.SockaddrCAN)
		if !ok || peer.Ifindex != c.ifIndex {
			continue
		}
		if len(body) < n {
			return 0, nil, errors.New("CAN ISO-TP read buffer is too small for complete PDU")
		}
		copy(body, wire[:n])
		return n, &CANISOTPAddr{Interface: c.local.Interface, RXID: c.local.TXID, TXID: c.local.RXID, FD: c.local.FD}, nil
	}
}

func (c *canISOTPNativePackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil || c.file == nil || c.raw == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	peer, ok := destination.(*CANISOTPAddr)
	if !ok || !validCANISOTPAddr(peer) || peer.Interface != c.local.Interface || peer.FD != c.local.FD ||
		peer.RXID != c.local.TXID || peer.TXID != c.local.RXID || len(body) == 0 || len(body) > canISOTPMaxPDU {
		return 0, errors.New("invalid CAN ISO-TP destination or PDU size")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	var written int
	var writeErr error
	err := c.raw.Write(func(fd uintptr) bool {
		for {
			written, writeErr = unix.SendmsgN(int(fd), body, nil, nil, unix.MSG_DONTWAIT)
			if errors.Is(writeErr, unix.EINTR) {
				continue
			}
			if errors.Is(writeErr, unix.EAGAIN) || errors.Is(writeErr, unix.EWOULDBLOCK) {
				return false
			}
			return true
		}
	})
	if err != nil {
		if c.closed.Load() || errors.Is(err, os.ErrClosed) {
			return 0, net.ErrClosed
		}
		return 0, err
	}
	if writeErr != nil {
		if c.closed.Load() || errors.Is(writeErr, unix.EBADF) {
			return 0, net.ErrClosed
		}
		return 0, writeErr
	}
	if written != len(body) {
		return 0, errors.New("short CAN ISO-TP datagram write")
	}
	return written, nil
}

func (c *canISOTPNativePackets) LocalAddr() net.Addr {
	if c == nil {
		return (*CANISOTPAddr)(nil)
	}
	return cloneCANISOTPAddr(c.local)
}

func (c *canISOTPNativePackets) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.closeErr = c.file.Close()
	})
	return c.closeErr
}

func (c *canISOTPNativePackets) SetDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.file.SetDeadline(deadline)
}

func (c *canISOTPNativePackets) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.file.SetReadDeadline(deadline)
}

func (c *canISOTPNativePackets) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.file == nil || c.closed.Load() {
		return net.ErrClosed
	}
	return c.file.SetWriteDeadline(deadline)
}

var _ net.PacketConn = (*canISOTPNativePackets)(nil)
