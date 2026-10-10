//go:build linux

package adapter

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/config"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

const udpLiteSendCoverage = 10
const udpLiteReceiveCoverage = 11

// ListenUDPLite refuses unsupported kernels; it never falls back to UDP.
func ListenUDPLite(address *net.UDPAddr) (*UDPLitePackets, error) {
	if address == nil {
		return nil, errors.New("missing UDP-Lite endpoint")
	}
	if err := (&config.UDPLite{ChecksumCoverage: "full"}).Validate(address.String()); err != nil {
		return nil, err
	}
	family := unix.AF_INET6
	if address.IP.To4() != nil {
		family = unix.AF_INET
	}
	fd, err := unix.Socket(family, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDPLITE)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "udp-lite")
	defer file.Close()
	for _, option := range []int{udpLiteSendCoverage, udpLiteReceiveCoverage} {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_UDPLITE, option, 0); err != nil {
			return nil, err
		}
		value, err := unix.GetsockoptInt(fd, unix.IPPROTO_UDPLITE, option)
		if err != nil {
			return nil, err
		}
		if value != 0 {
			return nil, errors.New("UDP-Lite full coverage was not applied")
		}
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BROADCAST, 0); err != nil {
		return nil, err
	}
	var bind unix.Sockaddr
	if family == unix.AF_INET {
		a := &unix.SockaddrInet4{Port: address.Port}
		copy(a.Addr[:], address.IP.To4())
		bind = a
	} else {
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1); err != nil {
			return nil, err
		}
		a := &unix.SockaddrInet6{Port: address.Port}
		copy(a.Addr[:], address.IP.To16())
		if address.Zone != "" {
			iface, err := net.InterfaceByName(address.Zone)
			if err != nil {
				return nil, err
			}
			a.ZoneId = uint32(iface.Index)
		}
		bind = a
	}
	if err := unix.Bind(fd, bind); err != nil {
		return nil, err
	}
	packets, err := net.FilePacketConn(file)
	if err != nil {
		return nil, err
	}
	conn, ok := packets.(*net.UDPConn)
	if !ok {
		packets.Close()
		return nil, errors.New("UDP-Lite descriptor cannot use bounded UDP packet ownership")
	}
	return &UDPLitePackets{conn: conn, ipv4: family == unix.AF_INET}, nil
}
