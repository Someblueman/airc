//go:build linux && arm64

package gateway_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	sctpTestSndInfo       = 2
	sctpTestRcvInfo       = 3
	sctpTestSndInfoBytes  = 16
	sctpTestRcvInfoBytes  = 28
	sctpTestUnordered     = 1
	sctpTestMessageLimit  = (16 << 10)
	sctpTestResponseLimit = ((64 << 10) + 1)
)

type sctpTestConn struct{ fd int }

func dialSCTP(t *testing.T, address string) *sctpTestConn {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	port, portErr := strconv.Atoi(portText)
	ip, ipErr := netip.ParseAddr(host)
	if err != nil || portErr != nil || ipErr != nil || port < 1 || port > 65535 || ip.Is4In6() {
		t.Fatalf("invalid SCTP test address %q: %v %v %v", address, err, portErr, ipErr)
	}
	family, peer := unix.AF_INET, unix.Sockaddr(nil)
	if ip.Is4() {
		peer = &unix.SockaddrInet4{Port: port, Addr: ip.As4()}
	} else {
		family = unix.AF_INET6
		peer = &unix.SockaddrInet6{Port: port, Addr: ip.As16()}
	}
	fd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.IPPROTO_SCTP)
	if err != nil {
		t.Fatalf("create native SCTP client socket: %v", err)
	}
	conn := &sctpTestConn{fd: fd}
	t.Cleanup(func() { _ = conn.close() })
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, 32, 1); err != nil {
		t.Fatalf("enable SCTP receive metadata: %v", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, 18, 0); err != nil {
		t.Fatalf("disable SCTP fragment interleave: %v", err)
	}
	if err := unix.Connect(fd, peer); err != nil && !errors.Is(err, unix.EINPROGRESS) && !errors.Is(err, unix.EALREADY) {
		t.Fatalf("connect native SCTP client: %v", err)
	}
	conn.wait(t, unix.POLLOUT, time.Now().Add(5*time.Second))
	soError, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
	if err != nil || soError != 0 {
		t.Fatalf("complete native SCTP connect: socket error=%d err=%v", soError, err)
	}
	return conn
}

func (c *sctpTestConn) close() error {
	if c == nil || c.fd < 0 {
		return nil
	}
	fd := c.fd
	c.fd = -1
	return unix.Close(fd)
}

func (c *sctpTestConn) sendRecord(t *testing.T, body []byte, unordered bool, deadline time.Time) error {
	t.Helper()
	if len(body) == 0 || len(body) > sctpTestMessageLimit {
		return fmt.Errorf("SCTP request record length %d outside 1..%d", len(body), sctpTestMessageLimit)
	}
	return c.sendRawRecord(t, body, unordered, deadline)
}

func (c *sctpTestConn) sendRawRecord(t *testing.T, body []byte, unordered bool, deadline time.Time) error {
	t.Helper()
	if len(body) == 0 {
		return errors.New("cannot send an empty SCTP record")
	}
	oob := make([]byte, unix.CmsgSpace(sctpTestSndInfoBytes))
	hdr := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
	hdr.Level, hdr.Type = unix.IPPROTO_SCTP, sctpTestSndInfo
	hdr.SetLen(unix.CmsgLen(sctpTestSndInfoBytes))
	data := oob[unix.CmsgLen(0):unix.CmsgLen(sctpTestSndInfoBytes)]
	// sctp_sndinfo: stream 0, ordered, PPID 0, no explicit association.
	if unordered {
		binary.LittleEndian.PutUint16(data[2:4], sctpTestUnordered)
	}
	for {
		n, err := unix.SendmsgN(c.fd, body, oob, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if err == nil {
			if n != len(body) {
				return io.ErrShortWrite
			}
			return nil
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return err
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err := c.wait(t, unix.POLLOUT, deadline); err != nil {
			return err
		}
	}
}
