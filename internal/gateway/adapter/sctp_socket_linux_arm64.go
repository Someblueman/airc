//go:build linux && arm64

package adapter

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"golang.org/x/sys/unix"
)

const (
	sctpOptionInitMessage        = 2
	sctpOptionFragmentInterleave = 18
	sctpOptionReceiveInfo        = 32
	sctpReceiveInfoCmsg          = 3
	sctpSendInfoCmsg             = 2
	sctpMessageUnordered         = 1
	sctpNotificationFlag         = 0x8000
	sctpReceiveInfoSize          = 28
	sctpRecordLimit              = MaxRequestFrameBytes
	sctpWriteRecordLimit         = auth.MaxResponseTokenBytes
)

var errSCTPRecordTooLarge = errors.New("sctp record exceeds limit")
var errSCTPReadBufferTooSmall = errors.New("sctp read buffer is smaller than record")

// ListenSCTP opens an ordered or unordered one-stream SCTP listener on an IPv4
// or IPv6 literal address. SCTP is restricted to Linux ARM64 until other native
// paths are separately qualified.
func ListenSCTP(address string, mode SCTPMode) (net.Listener, error) {
	if !validSCTPMode(mode) {
		return nil, errors.New("invalid sctp delivery mode")
	}
	addr, family, err := parseSCTPAddress(address)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.IPPROTO_SCTP)
	if err != nil {
		return nil, fmt.Errorf("create sctp socket: %w", err)
	}
	file := os.NewFile(uintptr(fd), "sctp-listener")
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	if err := configureSCTPSocket(fd, true); err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, addr); err != nil {
		return nil, fmt.Errorf("bind sctp socket: %w", err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		return nil, fmt.Errorf("listen sctp socket: %w", err)
	}
	local, err := unix.Getsockname(fd)
	if err != nil {
		return nil, fmt.Errorf("get sctp listener address: %w", err)
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("prepare sctp listener poller: %w", err)
	}
	failed = false
	return &sctpListener{file: file, raw: raw, addr: sctpNetAddr(local), mode: mode}, nil
}

type sctpListener struct {
	file      *os.File
	raw       syscall.RawConn
	addr      net.Addr
	mode      SCTPMode
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (l *sctpListener) Accept() (net.Conn, error) {
	var accepted int = -1
	var acceptErr error
	err := l.raw.Read(func(fd uintptr) bool {
		child, _, err := unix.Accept4(int(fd), unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return false
		}
		if err != nil {
			acceptErr = err
			return true
		}
		accepted = child
		return true
	})
	if err != nil {
		if l.closed.Load() || errors.Is(err, os.ErrClosed) {
			return nil, net.ErrClosed
		}
		return nil, fmt.Errorf("accept sctp connection: %w", err)
	}
	if acceptErr != nil {
		if l.closed.Load() || errors.Is(acceptErr, os.ErrClosed) || errors.Is(acceptErr, unix.EBADF) {
			return nil, net.ErrClosed
		}
		return nil, fmt.Errorf("accept sctp connection: %w", acceptErr)
	}
	file := os.NewFile(uintptr(accepted), "sctp-connection")
	if err := configureSCTPSocket(accepted, false); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	local, err := unix.Getsockname(accepted)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("get local sctp address: %w", err), file.Close())
	}
	remote, err := unix.Getpeername(accepted)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("get remote sctp address: %w", err), file.Close())
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("prepare sctp connection poller: %w", err), file.Close())
	}
	return &sctpConn{file: file, raw: raw, localAddr: sctpNetAddr(local), remoteAddr: sctpNetAddr(remote), mode: l.mode}, nil
}

func (l *sctpListener) Close() error {
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.closeErr = l.file.Close()
	})
	return l.closeErr
}

func (l *sctpListener) Addr() net.Addr { return l.addr }

var _ net.Listener = (*sctpListener)(nil)

type sctpConn struct {
	file       *os.File
	raw        syscall.RawConn
	localAddr  net.Addr
	remoteAddr net.Addr
	mode       SCTPMode
	readMu     sync.Mutex
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

func (c *sctpConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	message, err := c.readRecord()
	if err != nil {
		if errors.Is(err, errSCTPRecordTooLarge) || errors.Is(err, errSCTPInvalidRecord) {
			_ = c.Close()
		}
		return 0, err
	}
	if len(p) < len(message) {
		_ = c.Close()
		return 0, errSCTPReadBufferTooSmall
	}
	return copy(p, message), nil
}

var errSCTPInvalidRecord = errors.New("invalid sctp record")

func (c *sctpConn) readRecord() ([]byte, error) {
	message := make([]byte, 0, sctpRecordLimit)
	var identity *sctpReceiveInfo
	for {
		remaining := sctpRecordLimit - len(message) + 1
		fragment := make([]byte, remaining)
		oob := make([]byte, unix.CmsgSpace(sctpReceiveInfoSize))
		var n, oobn, flags int
		var receiveErr error
		err := c.raw.Read(func(fd uintptr) bool {
			n, oobn, flags, _, receiveErr = unix.Recvmsg(int(fd), fragment, oob, unix.MSG_DONTWAIT)
			if errors.Is(receiveErr, unix.EAGAIN) || errors.Is(receiveErr, unix.EWOULDBLOCK) {
				return false
			}
			return true
		})
		if err != nil {
			return nil, err
		}
		if receiveErr != nil {
			if errors.Is(receiveErr, unix.ECONNRESET) || errors.Is(receiveErr, unix.ENOTCONN) {
				return nil, io.EOF
			}
			return nil, receiveErr
		}
		if n == 0 {
			return nil, io.EOF
		}
		if flags&unix.MSG_TRUNC != 0 || flags&unix.MSG_CTRUNC != 0 || flags&sctpNotificationFlag != 0 {
			return nil, errSCTPInvalidRecord
		}
		if n > remaining-1 || len(message)+n > sctpRecordLimit {
			return nil, errSCTPRecordTooLarge
		}
		info, err := parseSCTPReceiveInfo(oob[:oobn], c.mode)
		if err != nil {
			return nil, err
		}
		if identity == nil {
			identity = &info
		} else if *identity != info {
			return nil, errSCTPInvalidRecord
		}
		message = append(message, fragment[:n]...)
		if flags&unix.MSG_EOR != 0 {
			return message, nil
		}
	}
}

// sctpReceiveInfo contains the SCTP_RCVINFO fields stable across fragments of
// one user message. TSN and cumulative TSN are omitted because they can change
// as the stack delivers different chunks of that message.
type sctpReceiveInfo struct {
	stream      uint16
	sequence    uint16
	flags       uint16
	ppid        uint32
	context     uint32
	association int32
}

func parseSCTPReceiveInfo(oob []byte, mode SCTPMode) (sctpReceiveInfo, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return sctpReceiveInfo{}, errors.Join(errSCTPInvalidRecord, fmt.Errorf("parse sctp receive metadata: %w", err))
	}
	if len(messages) != 1 {
		return sctpReceiveInfo{}, errSCTPInvalidRecord
	}
	control := messages[0]
	if control.Header.Level != unix.IPPROTO_SCTP || control.Header.Type != sctpReceiveInfoCmsg || len(control.Data) != sctpReceiveInfoSize {
		return sctpReceiveInfo{}, errSCTPInvalidRecord
	}
	return parseSCTPReceiveInfoData(control.Data, mode)
}

func parseSCTPReceiveInfoData(data []byte, mode SCTPMode) (sctpReceiveInfo, error) {
	if len(data) != sctpReceiveInfoSize {
		return sctpReceiveInfo{}, errSCTPInvalidRecord
	}
	info := sctpReceiveInfo{
		stream:      binary.LittleEndian.Uint16(data[0:2]),
		sequence:    binary.LittleEndian.Uint16(data[2:4]),
		flags:       binary.LittleEndian.Uint16(data[4:6]),
		ppid:        binary.BigEndian.Uint32(data[8:12]),
		context:     binary.LittleEndian.Uint32(data[20:24]),
		association: int32(binary.LittleEndian.Uint32(data[24:28])),
	}
	wantFlags := uint16(0)
	if mode == SCTPUnordered {
		wantFlags = sctpMessageUnordered
	}
	if !validSCTPMode(mode) || info.stream != 0 || info.flags != wantFlags || info.ppid != 0 {
		return sctpReceiveInfo{}, errSCTPInvalidRecord
	}
	return info, nil
}

func (c *sctpConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > sctpWriteRecordLimit {
		return 0, errSCTPRecordTooLarge
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var n int
	var sendErr error
	var control []byte
	if c.mode == SCTPUnordered {
		control = sctpSendInfo(sctpMessageUnordered)
	}
	err := c.raw.Write(func(fd uintptr) bool {
		n, sendErr = unix.SendmsgN(int(fd), p, control, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if errors.Is(sendErr, unix.EAGAIN) || errors.Is(sendErr, unix.EWOULDBLOCK) {
			return false
		}
		return true
	})
	if err != nil {
		return 0, err
	}
	if sendErr != nil {
		return 0, sendErr
	}
	if n != len(p) {
		_ = c.Close()
		return n, io.ErrShortWrite
	}
	return n, nil
}

func sctpSendInfo(flags uint16) []byte {
	// Linux ARM64 cmsghdr is size_t + two int32 values; SCTP_SNDINFO is 16 bytes.
	const sndInfoSize = 16
	oob := make([]byte, unix.CmsgSpace(sndInfoSize))
	binary.LittleEndian.PutUint64(oob[0:8], uint64(unix.CmsgLen(sndInfoSize)))
	binary.LittleEndian.PutUint32(oob[8:12], uint32(unix.IPPROTO_SCTP))
	binary.LittleEndian.PutUint32(oob[12:16], uint32(sctpSendInfoCmsg))
	// sinfo_stream, sinfo_flags, PPID, context and association ID.
	binary.LittleEndian.PutUint16(oob[unix.CmsgLen(0)+2:unix.CmsgLen(0)+4], flags)
	return oob
}

func validSCTPMode(mode SCTPMode) bool {
	return mode == SCTPOrdered || mode == SCTPUnordered
}

func (c *sctpConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.file.Close() })
	return c.closeErr
}

func (c *sctpConn) LocalAddr() net.Addr                { return c.localAddr }
func (c *sctpConn) RemoteAddr() net.Addr               { return c.remoteAddr }
func (c *sctpConn) SetDeadline(t time.Time) error      { return c.file.SetDeadline(t) }
func (c *sctpConn) SetReadDeadline(t time.Time) error  { return c.file.SetReadDeadline(t) }
func (c *sctpConn) SetWriteDeadline(t time.Time) error { return c.file.SetWriteDeadline(t) }

var _ net.Conn = (*sctpConn)(nil)

func configureSCTPSocket(fd int, listener bool) error {
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, sctpOptionReceiveInfo, 1); err != nil {
		return fmt.Errorf("enable sctp receive metadata: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, sctpOptionFragmentInterleave, 0); err != nil {
		return fmt.Errorf("disable sctp fragment interleave: %w", err)
	}
	if listener {
		// Linux UAPI struct sctp_initmsg is four native-endian uint16 values.
		var init [8]byte
		binary.LittleEndian.PutUint16(init[0:2], 1)
		binary.LittleEndian.PutUint16(init[2:4], 1)
		if err := unix.SetsockoptString(fd, unix.IPPROTO_SCTP, sctpOptionInitMessage, string(init[:])); err != nil {
			return fmt.Errorf("configure one-stream sctp association: %w", err)
		}
	}
	return nil
}

func parseSCTPAddress(address string) (unix.Sockaddr, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, 0, fmt.Errorf("parse sctp listen address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return nil, 0, errors.New("invalid sctp port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, 0, errors.New("sctp listen address must use an ip literal")
	}
	if ip.Is4() {
		return &unix.SockaddrInet4{Port: port, Addr: ip.As4()}, unix.AF_INET, nil
	}
	if !ip.Is6() || ip.Is4In6() {
		return nil, 0, errors.New("unsupported sctp ip address")
	}
	zone := ip.Zone()
	var zoneID uint32
	if zone != "" {
		iface, err := net.InterfaceByName(zone)
		if err != nil {
			return nil, 0, fmt.Errorf("resolve sctp ipv6 zone: %w", err)
		}
		zoneID = uint32(iface.Index)
		ip = ip.WithZone("")
	}
	return &unix.SockaddrInet6{Port: port, Addr: ip.As16(), ZoneId: zoneID}, unix.AF_INET6, nil
}

func sctpNetAddr(addr unix.Sockaddr) net.Addr {
	switch a := addr.(type) {
	case *unix.SockaddrInet4:
		return sctpIPAddr{ip: net.IP(a.Addr[:]), port: a.Port}
	case *unix.SockaddrInet6:
		zone := ""
		if a.ZoneId != 0 {
			if iface, err := net.InterfaceByIndex(int(a.ZoneId)); err == nil {
				zone = iface.Name
			}
		}
		ip := netip.AddrFrom16(a.Addr).WithZone(zone)
		return sctpIPAddr{ip: net.IP(ip.AsSlice()), port: a.Port, zone: zone}
	default:
		return sctpAddr(fmt.Sprint(addr))
	}
}

type sctpAddr string

func (s sctpAddr) Network() string { return "sctp" }
func (s sctpAddr) String() string  { return string(s) }

type sctpIPAddr struct {
	ip   net.IP
	port int
	zone string
}

func (a sctpIPAddr) Network() string { return "sctp" }
func (a sctpIPAddr) String() string {
	return (&net.TCPAddr{IP: a.ip, Port: a.port, Zone: a.zone}).String()
}
