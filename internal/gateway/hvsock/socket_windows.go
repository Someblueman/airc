//go:build windows

package hvsock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	afHyperV     = 34
	hvRaw        = 1
	sockStream   = 1
	fionbio      = 0x8004667e
	solSocket    = 0xffff
	soError      = 0x1007
	shutdownSend = 1
)

var (
	winsockOnce    sync.Once
	winsockErr     error
	ws2            = windows.NewLazySystemDLL("Ws2_32.dll")
	procBind       = ws2.NewProc("bind")
	procConnect    = ws2.NewProc("connect")
	procListen     = ws2.NewProc("listen")
	procAccept     = ws2.NewProc("accept")
	procPeer       = ws2.NewProc("getpeername")
	procName       = ws2.NewProc("getsockname")
	procIoctl      = ws2.NewProc("ioctlsocket")
	procGetSockopt = ws2.NewProc("getsockopt")
)

type sockaddrHV struct {
	Family    uint16
	Reserved  uint16
	VMID      windows.GUID
	ServiceID windows.GUID
}

type HyperVAddr struct {
	VMID      string
	ServiceID string
}

func (a *HyperVAddr) Network() string { return "hvsock" }
func (a *HyperVAddr) String() string  { return a.VMID + ":" + a.ServiceID }

func initWinsock() error {
	winsockOnce.Do(func() {
		var data windows.WSAData
		winsockErr = windows.WSAStartup(0x0202, &data)
		if winsockErr == nil && data.Version != 0x0202 {
			winsockErr = fmt.Errorf("Winsock 2.2 unavailable (negotiated %#x)", data.Version)
		}
	})
	return winsockErr
}

//go:uintptrescapes
func rawCall(p *windows.LazyProc, args ...uintptr) (uintptr, error) {
	r, _, err := p.Call(args...)
	if int32(r) == -1 {
		if err == nil || errors.Is(err, syscall.Errno(0)) {
			return r, errors.New("Winsock call failed without an error code")
		}
		return r, err
	}
	return r, nil
}

//go:uintptrescapes
func rawSocketCall(p *windows.LazyProc, args ...uintptr) (windows.Handle, error) {
	r, _, err := p.Call(args...)
	if r == ^uintptr(0) {
		if err == nil || errors.Is(err, syscall.Errno(0)) {
			return windows.InvalidHandle, errors.New("Winsock call failed without an error code")
		}
		return windows.InvalidHandle, err
	}
	return windows.Handle(r), nil
}

func guidString(g windows.GUID) string { return strings.ToLower(strings.Trim(g.String(), "{}")) }

func parseGUID(s string) (windows.GUID, error) {
	g, err := windows.GUIDFromString("{" + s + "}")
	if err != nil {
		return windows.GUID{}, err
	}
	if guidString(g) != s {
		return windows.GUID{}, fmt.Errorf("noncanonical UUID")
	}
	return g, nil
}

func newHVSocket() (windows.Handle, error) {
	if err := initWinsock(); err != nil {
		return windows.InvalidHandle, err
	}
	h, err := windows.WSASocket(afHyperV, sockStream, hvRaw, nil, 0, windows.WSA_FLAG_NO_HANDLE_INHERIT)
	if err != nil {
		return windows.InvalidHandle, err
	}
	if err := setNonblocking(h); err != nil {
		_ = windows.Closesocket(h)
		return windows.InvalidHandle, err
	}
	return h, nil
}

func setNonblocking(h windows.Handle) error {
	var enabled uint32 = 1
	_, err := rawCall(procIoctl, uintptr(h), fionbio, uintptr(unsafe.Pointer(&enabled)))
	runtime.KeepAlive(&enabled)
	return err
}

func makeHVAddress(vm, service string) (sockaddrHV, error) {
	vg, err := parseGUID(vm)
	if err != nil {
		return sockaddrHV{}, err
	}
	sg, err := parseGUID(service)
	if err != nil {
		return sockaddrHV{}, err
	}
	return sockaddrHV{Family: afHyperV, VMID: vg, ServiceID: sg}, nil
}

func getHVName(h windows.Handle, p *windows.LazyProc) (sockaddrHV, error) {
	var addr sockaddrHV
	n := int32(unsafe.Sizeof(addr))
	_, err := rawCall(p, uintptr(h), uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&n)))
	runtime.KeepAlive(&addr)
	if err != nil {
		return sockaddrHV{}, err
	}
	if n != int32(unsafe.Sizeof(addr)) || addr.Family != afHyperV || addr.Reserved != 0 {
		return sockaddrHV{}, fmt.Errorf("invalid Hyper-V socket address")
	}
	return addr, nil
}

func guidEqual(a windows.GUID, b string) bool { return guidString(a) == b }

func hvAddr(a sockaddrHV) *HyperVAddr {
	return &HyperVAddr{VMID: guidString(a.VMID), ServiceID: guidString(a.ServiceID)}
}

type listener struct {
	mu             sync.Mutex
	h              windows.Handle
	closed         bool
	guest, service string
	done           chan struct{}
	stopCancel     func() bool
	cancelDone     chan struct{}
}

func Listen(ctx context.Context, guestVM, service string) (net.Listener, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateGuest(guestVM); err != nil {
		return nil, err
	}
	if err := ValidateService(service); err != nil {
		return nil, err
	}
	addr, err := makeHVAddress(guestVM, service)
	if err != nil {
		return nil, err
	}
	h, err := newHVSocket()
	if err != nil {
		return nil, err
	}
	if _, err = rawCall(procBind, uintptr(h), uintptr(unsafe.Pointer(&addr)), unsafe.Sizeof(addr)); err != nil {
		_ = windows.Closesocket(h)
		return nil, fmt.Errorf("bind Hyper-V socket: %w", err)
	}
	if _, err = rawCall(procListen, uintptr(h), 1); err != nil {
		_ = windows.Closesocket(h)
		return nil, fmt.Errorf("listen Hyper-V socket: %w", err)
	}
	bound, err := getHVName(h, procName)
	if err != nil || !guidEqual(bound.VMID, guestVM) || !guidEqual(bound.ServiceID, service) {
		_ = windows.Closesocket(h)
		if err == nil {
			err = fmt.Errorf("listener bound to unexpected endpoint")
		}
		return nil, err
	}
	l := &listener{h: h, guest: guestVM, service: service, done: make(chan struct{}), cancelDone: make(chan struct{})}
	l.stopCancel = context.AfterFunc(ctx, func() {
		defer close(l.cancelDone)
		_ = l.closeHandle()
	})
	if err := ctx.Err(); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		select {
		case <-l.done:
			return nil, net.ErrClosed
		default:
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, net.ErrClosed
		}
		h := l.h
		var peer sockaddrHV
		n := int32(unsafe.Sizeof(peer))
		accepted, err := rawSocketCall(procAccept, uintptr(h), uintptr(unsafe.Pointer(&peer)), uintptr(unsafe.Pointer(&n)))
		l.mu.Unlock()
		if err == nil {
			ch := accepted
			if err := windows.SetHandleInformation(ch, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
				_ = windows.Closesocket(ch)
				continue
			}
			if n != int32(unsafe.Sizeof(peer)) || peer.Family != afHyperV || peer.Reserved != 0 || !guidEqual(peer.VMID, l.guest) {
				_ = windows.Closesocket(ch)
				continue
			}
			if e := setNonblocking(ch); e != nil {
				_ = windows.Closesocket(ch)
				continue
			}
			local, e := getHVName(ch, procName)
			if e != nil {
				_ = windows.Closesocket(ch)
				continue
			}
			l.mu.Lock()
			if l.closed {
				l.mu.Unlock()
				_ = windows.Closesocket(ch)
				return nil, net.ErrClosed
			}
			l.mu.Unlock()
			return newConn(ch, local, peer), nil
		}
		if !isWouldBlock(err) {
			return nil, err
		}
		if err := waitListener(l.done); err != nil {
			return nil, err
		}
	}
}

func (l *listener) Close() error {
	err := l.closeHandle()
	if l.stopCancel != nil {
		if l.stopCancel() {
			close(l.cancelDone)
		} else {
			<-l.cancelDone
		}
	}
	return err
}

func (l *listener) closeHandle() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	close(l.done)
	return windows.Closesocket(l.h)
}

func (l *listener) Addr() net.Addr { return &HyperVAddr{VMID: l.guest, ServiceID: l.service} }

func Dial(ctx context.Context, service string) (net.Conn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if err := ValidateService(service); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addr, err := makeHVAddress(guidParent, service)
	if err != nil {
		return nil, err
	}
	h, err := newHVSocket()
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = windows.Closesocket(h)
		}
	}()
	_, err = rawCall(procConnect, uintptr(h), uintptr(unsafe.Pointer(&addr)), unsafe.Sizeof(addr))
	runtime.KeepAlive(&addr)
	if err != nil && !isWouldBlock(err) && !errors.Is(err, windows.WSAEINPROGRESS) && !errors.Is(err, windows.WSAEALREADY) {
		return nil, fmt.Errorf("connect Hyper-V socket: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		soerr, e := socketError(h)
		if e != nil {
			return nil, e
		}
		if soerr != 0 {
			return nil, syscall.Errno(soerr)
		}
		peer, peerErr := getHVName(h, procPeer)
		if peerErr == nil {
			local, e := getHVName(h, procName)
			if e != nil {
				return nil, e
			}
			closed = true
			return newConn(h, local, peer), nil
		}
		if !isWouldBlock(peerErr) && !errors.Is(peerErr, windows.WSAENOTCONN) {
			return nil, peerErr
		}
		if err := wait(ctx, time.Time{}); err != nil {
			return nil, err
		}
	}
}

func socketError(h windows.Handle) (int32, error) {
	var value int32
	n := int32(unsafe.Sizeof(value))
	_, err := rawCall(procGetSockopt, uintptr(h), solSocket, soError, uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&n)))
	if err != nil {
		return 0, err
	}
	if n != int32(unsafe.Sizeof(value)) {
		return 0, fmt.Errorf("invalid SO_ERROR length")
	}
	return value, nil
}

func isWouldBlock(err error) bool { return errors.Is(err, windows.WSAEWOULDBLOCK) }

func wait(ctx context.Context, deadline time.Time) error {
	d := 50 * time.Millisecond
	if !deadline.IsZero() {
		left := time.Until(deadline)
		if left <= 0 {
			return timeoutError{}
		}
		if left < d {
			d = left
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func waitListener(done <-chan struct{}) error {
	t := time.NewTimer(50 * time.Millisecond)
	defer t.Stop()
	select {
	case <-done:
		return net.ErrClosed
	case <-t.C:
		return nil
	}
}

var _ net.Listener = (*listener)(nil)
var _ net.Conn = (*conn)(nil)
