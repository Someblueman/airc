//go:build linux

package adapter

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	rawEthernetProtocol      = 0x88b5
	rawEthernetHeaderBytes   = 10
	rawEthernetMaxDatagram   = 1000
	rawEthernetMinPayload    = 46
	rawEthernetReceiveBuffer = 256 << 10
)

type RawEthernetPackets struct {
	file      *os.File
	raw       syscall.RawConn
	local     RawEthernetAddr
	ifIndex   int
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

func ListenRawEthernet(address *RawEthernetAddr, interfaceName string) (*RawEthernetPackets, error) {
	if !validRawEthernetAddr(address) || interfaceName == "" {
		return nil, errors.New("invalid raw Ethernet listener endpoint")
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}
	if iface.Index <= 0 || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 ||
		!validRawEthernetMAC(iface.HardwareAddr) || !bytes.Equal(iface.HardwareAddr, address.MAC) {
		return nil, errors.New("raw Ethernet interface is not up or does not own the configured MAC")
	}

	protocol := rawEthernetNetworkOrder(rawEthernetProtocol)
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, int(protocol))
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "raw-ethernet")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap raw Ethernet socket")
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	if err := setRawEthernetReceiveBuffer(fd); err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: protocol, Ifindex: iface.Index}); err != nil {
		return nil, err
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}
	closeOnError = false
	return &RawEthernetPackets{
		file:    file,
		raw:     raw,
		local:   RawEthernetAddr{MAC: append(net.HardwareAddr(nil), address.MAC...), Channel: address.Channel},
		ifIndex: iface.Index,
	}, nil
}

func rawEthernetNetworkOrder(protocol uint16) uint16 {
	var wire [2]byte
	binary.BigEndian.PutUint16(wire[:], protocol)
	return binary.NativeEndian.Uint16(wire[:])
}

func setRawEthernetReceiveBuffer(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, rawEthernetReceiveBuffer/2); err != nil {
		return err
	}
	effective, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF)
	if err != nil {
		return err
	}
	if effective != rawEthernetReceiveBuffer {
		return errors.New("kernel raw Ethernet receive queue does not match configured bound")
	}
	return nil
}

func (c *RawEthernetPackets) ReadFrom(body []byte) (int, net.Addr, error) {
	if c == nil || c.file == nil || c.raw == nil || c.closed.Load() {
		return 0, nil, net.ErrClosed
	}
	var wire [rawEthernetHeaderBytes + rawEthernetMaxDatagram + 1]byte
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
			if errors.Is(receiveErr, unix.EINTR) {
				continue
			}
			if c.closed.Load() || errors.Is(receiveErr, unix.EBADF) {
				return 0, nil, net.ErrClosed
			}
			return 0, nil, receiveErr
		}
		if n <= 0 || n > len(wire) || oobn != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			continue
		}
		link, ok := source.(*unix.SockaddrLinklayer)
		if !ok || link.Ifindex != c.ifIndex || link.Protocol != rawEthernetNetworkOrder(rawEthernetProtocol) ||
			link.Hatype != unix.ARPHRD_ETHER || link.Halen != 6 || link.Pkttype != unix.PACKET_HOST {
			continue
		}
		sourceMAC := net.HardwareAddr(link.Addr[:6])
		if !validRawEthernetMAC(sourceMAC) {
			continue
		}
		bodyLength, ok := c.parsePayload(wire[:n])
		if !ok {
			continue
		}
		if len(body) < bodyLength {
			return 0, nil, errors.New("raw Ethernet read buffer is too small for complete datagram")
		}
		copy(body, wire[rawEthernetHeaderBytes:rawEthernetHeaderBytes+bodyLength])
		return bodyLength, &RawEthernetAddr{MAC: append(net.HardwareAddr(nil), sourceMAC...), Channel: binary.BigEndian.Uint16(wire[6:8])}, nil
	}
}

func (c *RawEthernetPackets) parsePayload(frame []byte) (int, bool) {
	if len(frame) < rawEthernetHeaderBytes+1 || len(frame) > rawEthernetHeaderBytes+rawEthernetMaxDatagram ||
		string(frame[:4]) != "AGE1" || binary.BigEndian.Uint16(frame[4:6]) != c.local.Channel {
		return 0, false
	}
	sourceChannel := binary.BigEndian.Uint16(frame[6:8])
	bodyLength := int(binary.BigEndian.Uint16(frame[8:10]))
	declared := rawEthernetHeaderBytes + bodyLength
	if sourceChannel == 0 || bodyLength < 1 || bodyLength > rawEthernetMaxDatagram {
		return 0, false
	}
	if len(frame) == declared {
		return bodyLength, true
	}
	if declared >= rawEthernetMinPayload || len(frame) != rawEthernetMinPayload {
		return 0, false
	}
	for _, value := range frame[declared:] {
		if value != 0 {
			return 0, false
		}
	}
	return bodyLength, true
}

func (c *RawEthernetPackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if c == nil || c.file == nil || c.raw == nil || c.closed.Load() {
		return 0, net.ErrClosed
	}
	peer, ok := destination.(*RawEthernetAddr)
	if !ok || !validRawEthernetAddr(peer) || len(body) == 0 || len(body) > rawEthernetMaxDatagram ||
		(bytes.Equal(peer.MAC, c.local.MAC) && peer.Channel == c.local.Channel) {
		return 0, errors.New("invalid raw Ethernet destination or datagram size")
	}
	frameLength := rawEthernetHeaderBytes + len(body)
	if frameLength < rawEthernetMinPayload {
		frameLength = rawEthernetMinPayload
	}
	frame := make([]byte, frameLength)
	copy(frame[:4], "AGE1")
	binary.BigEndian.PutUint16(frame[4:6], peer.Channel)
	binary.BigEndian.PutUint16(frame[6:8], c.local.Channel)
	binary.BigEndian.PutUint16(frame[8:10], uint16(len(body)))
	copy(frame[rawEthernetHeaderBytes:], body)
	destinationAddress := &unix.SockaddrLinklayer{
		Protocol: rawEthernetNetworkOrder(rawEthernetProtocol),
		Ifindex:  c.ifIndex,
		Hatype:   unix.ARPHRD_ETHER,
		Halen:    6,
	}
	copy(destinationAddress.Addr[:6], peer.MAC)
	var written int
	var writeErr error
	err := c.raw.Write(func(fd uintptr) bool {
		for {
			written, writeErr = unix.SendmsgN(int(fd), frame, nil, destinationAddress, unix.MSG_DONTWAIT)
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
	if written != len(frame) {
		return 0, errors.New("short raw Ethernet datagram write")
	}
	return len(body), nil
}

func (c *RawEthernetPackets) LocalAddr() net.Addr {
	if c == nil {
		return (*RawEthernetAddr)(nil)
	}
	return &RawEthernetAddr{MAC: append(net.HardwareAddr(nil), c.local.MAC...), Channel: c.local.Channel}
}

func (c *RawEthernetPackets) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.closeErr = c.file.Close()
	})
	return c.closeErr
}

func (c *RawEthernetPackets) SetDeadline(deadline time.Time) error {
	if c == nil || c.file == nil {
		return net.ErrClosed
	}
	return c.file.SetDeadline(deadline)
}

func (c *RawEthernetPackets) SetReadDeadline(deadline time.Time) error {
	if c == nil || c.file == nil {
		return net.ErrClosed
	}
	return c.file.SetReadDeadline(deadline)
}

func (c *RawEthernetPackets) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.file == nil {
		return net.ErrClosed
	}
	return c.file.SetWriteDeadline(deadline)
}

var _ net.PacketConn = (*RawEthernetPackets)(nil)
