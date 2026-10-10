//go:build linux && arm64

package gateway_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
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

type sctpTestConn struct {
	fd   int
	mode adapter.SCTPMode
}

func dialSCTP(t *testing.T, address string, mode adapter.SCTPMode) *sctpTestConn {
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
	conn := &sctpTestConn{fd: fd, mode: mode}
	t.Cleanup(func() { _ = conn.close() })
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, 32, 1); err != nil {
		t.Fatalf("enable SCTP receive metadata: %v", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, 18, 0); err != nil {
		t.Fatalf("disable SCTP fragment interleave: %v", err)
	}
	if mode == adapter.SCTPOrderedTTL {
		if err := setSCTPPRSupport(fd); err != nil {
			t.Fatalf("enable SCTP partial reliability: %v", err)
		}
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, 13, 512); err != nil {
			t.Fatalf("set partial-reliable client SCTP_MAXSEG: %v", err)
		}
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 8<<20); err != nil {
			t.Fatalf("set bounded partial-reliable send buffer: %v", err)
		}
	}
	if err := unix.Connect(fd, peer); err != nil && !errors.Is(err, unix.EINPROGRESS) && !errors.Is(err, unix.EALREADY) {
		t.Fatalf("connect native SCTP client: %v", err)
	}
	if err := conn.wait(t, unix.POLLOUT, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("poll native SCTP connect: %v", err)
	}
	soError, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
	if err != nil || soError != 0 {
		t.Fatalf("complete native SCTP connect: socket error=%d err=%v", soError, err)
	}
	if mode == adapter.SCTPOrderedTTL {
		enabled, err := querySCTPPRSupport(fd)
		if err != nil || !enabled {
			t.Fatalf("peer did not negotiate SCTP partial reliability: enabled=%v err=%v", enabled, err)
		}
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

func (c *sctpTestConn) sendRecord(t *testing.T, body []byte, deadline time.Time) error {
	t.Helper()
	if len(body) == 0 || len(body) > sctpTestMessageLimit {
		return fmt.Errorf("SCTP request record length %d outside 1..%d", len(body), sctpTestMessageLimit)
	}
	return c.sendRawRecord(t, body, c.mode, deadline)
}

func (c *sctpTestConn) sendRawRecord(t *testing.T, body []byte, mode adapter.SCTPMode, deadline time.Time) error {
	t.Helper()
	return c.sendRecordMode(t, body, mode, mode == adapter.SCTPOrderedTTL, deadline)
}

func (c *sctpTestConn) sendRecordMode(t *testing.T, body []byte, mode adapter.SCTPMode, partialReliable bool, deadline time.Time) error {
	t.Helper()
	if len(body) == 0 {
		return errors.New("cannot send an empty SCTP record")
	}
	if mode != adapter.SCTPOrdered && mode != adapter.SCTPUnordered && mode != adapter.SCTPOrderedTTL {
		return fmt.Errorf("invalid SCTP test mode %d", mode)
	}
	for {
		sent, err := c.trySendRecord(body, mode, partialReliable)
		if err != nil {
			if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
				return err
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
		} else if sent {
			return nil
		}
		if err := c.wait(t, unix.POLLOUT, deadline); err != nil {
			return err
		}
	}
}

func (c *sctpTestConn) trySendRecord(body []byte, mode adapter.SCTPMode, partialReliable bool) (bool, error) {
	if len(body) == 0 {
		return false, errors.New("cannot send an empty SCTP record")
	}
	n, err := unix.SendmsgN(c.fd, body, sctpSendControl(mode, partialReliable), nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	if err != nil {
		return false, err
	}
	if n != len(body) {
		return false, io.ErrShortWrite
	}
	return true, nil
}
