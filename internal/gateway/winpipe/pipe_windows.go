//go:build windows

// Package winpipe provides a bounded, authenticated local named-pipe stream.
package winpipe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	maxFrame       = 65536
	requestBuffer  = 16388
	responseBuffer = 65540
	clientAccess   = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.FILE_WRITE_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE
)

var (
	advapi32                       = windows.NewLazySystemDLL("advapi32.dll")
	impersonateNamedPipeClientProc = advapi32.NewProc("ImpersonateNamedPipeClient")
)

// Conn is a sequential, single-request named-pipe connection. Do not issue
// concurrent I/O calls; each method joins its overlapped operation on return.
type Conn struct {
	handle      windows.Handle
	server      bool
	clientSID   string
	closed      bool
	readRequest bool
	verified    bool
}

// Accept creates and waits for one protected server instance.
func Accept(ctx context.Context, name, serverSID, clientSID string) (*Conn, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	pipeName, owner, client, err := validate(name, serverSID, clientSID)
	if err != nil {
		return nil, err
	}
	if err := requireCurrentSID(owner); err != nil {
		return nil, err
	}
	sd, err := descriptor(owner, client)
	if err != nil {
		return nil, err
	}
	name16, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return nil, errors.New("invalid named pipe path")
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateNamedPipe(
		name16,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, responseBuffer, requestBuffer, 0, &sa,
	)
	if err != nil {
		return nil, fmt.Errorf("create named pipe: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = windows.CloseHandle(h)
		}
	}()
	if err := connect(ctx, h); err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := verifyDescriptor(h, owner, client); err != nil {
		return nil, err
	}
	keep = true
	return &Conn{handle: h, server: true, clientSID: client}, nil
}

// Dial opens a protected server pipe, retrying only while it is absent or busy.
func Dial(ctx context.Context, name, serverSID, clientSID string) (*Conn, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	pipeName, owner, client, err := validate(name, serverSID, clientSID)
	if err != nil {
		return nil, err
	}
	if err := requireCurrentSID(client); err != nil {
		return nil, err
	}
	path, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return nil, errors.New("invalid named pipe path")
	}
	for {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		h, openErr := windows.CreateFile(path, clientAccess, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if openErr == nil {
			if err := checkContext(ctx); err != nil {
				_ = windows.CloseHandle(h)
				return nil, err
			}
			if err := verifyDescriptor(h, owner, client); err != nil {
				_ = windows.CloseHandle(h)
				return nil, err
			}
			return &Conn{handle: h, clientSID: client}, nil
		}
		if !errors.Is(openErr, windows.ERROR_PIPE_BUSY) && !errors.Is(openErr, windows.ERROR_FILE_NOT_FOUND) {
			return nil, fmt.Errorf("open named pipe: %w", openErr)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// ReadFrame reads one four-byte big-endian length-prefixed frame.
func (c *Conn) ReadFrame(ctx context.Context, max uint32) ([]byte, error) {
	if c.closed || max == 0 || max > maxFrame || (c.server && c.verified) {
		return nil, errors.New("invalid named pipe read state or bound")
	}
	var header [4]byte
	if err := c.readFull(ctx, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > max {
		return nil, errors.New("named pipe frame length is outside its bound")
	}
	frame := make([]byte, int(n))
	if err := c.readFull(ctx, frame); err != nil {
		return nil, err
	}
	if c.server {
		c.readRequest = true
	}
	return frame, nil
}

// WriteFrame writes one bounded frame, retrying partial overlapped writes.
func (c *Conn) WriteFrame(ctx context.Context, data []byte, max uint32) error {
	if c.closed || max == 0 || max > maxFrame || len(data) == 0 || uint64(len(data)) > uint64(max) || (c.server && !c.verified) {
		return errors.New("invalid named pipe write state or bound")
	}
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	if err := c.writeFull(ctx, buf); err != nil {
		return err
	}
	if c.server {
		c.verified = false
		c.readRequest = false
	}
	return nil
}

// VerifyClient authenticates the server-side peer after its complete frame was read.
func (c *Conn) VerifyClient() error {
	if c.closed || !c.server || !c.readRequest || c.verified {
		return errors.New("named pipe client verification is out of sequence")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ret, _, callErr := impersonateNamedPipeClientProc.Call(uintptr(c.handle))
	if ret == 0 {
		return fmt.Errorf("impersonate named pipe client: %w", callErr)
	}
	var token windows.Token
	tokenErr := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token)
	var got string
	if tokenErr == nil {
		user, err := token.GetTokenUser()
		if err != nil {
			tokenErr = err
		} else {
			got = user.User.Sid.String()
		}
		if err := token.Close(); err != nil && tokenErr == nil {
			tokenErr = err
		}
	}
	revertErr := windows.RevertToSelf()
	if revertErr != nil {
		os.Exit(1)
	}
	if tokenErr != nil {
		return fmt.Errorf("inspect named pipe client token: %w", tokenErr)
	}
	if got != c.clientSID {
		return errors.New("named pipe client identity does not match")
	}
	c.verified = true
	return nil
}

// Close closes the pipe handle. It is safe to call more than once.
func (c *Conn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return windows.CloseHandle(c.handle)
}

func (c *Conn) readFull(ctx context.Context, dst []byte) error {
	for len(dst) > 0 {
		n, err := c.transfer(ctx, dst, false)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("named pipe ended during frame")
		}
		dst = dst[n:]
	}
	return nil
}

func (c *Conn) writeFull(ctx context.Context, src []byte) error {
	for len(src) > 0 {
		n, err := c.transfer(ctx, src, true)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("named pipe accepted an empty write")
		}
		src = src[n:]
	}
	return nil
}

func (c *Conn) transfer(ctx context.Context, p []byte, write bool) (int, error) {
	if err := checkContext(ctx); err != nil {
		return 0, err
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("create named pipe I/O event: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	var done uint32
	var ioErr error
	if write {
		ioErr = windows.WriteFile(c.handle, p, &done, &overlapped)
	} else {
		ioErr = windows.ReadFile(c.handle, p, &done, &overlapped)
	}
	if ioErr == nil {
		// Use the OVERLAPPED result even for synchronous completion; the
		// immediate byte-count output is not the asynchronous I/O contract.
		if err := windows.GetOverlappedResult(c.handle, &overlapped, &done, true); err != nil {
			return 0, fmt.Errorf("complete named pipe I/O: %w", err)
		}
		runtime.KeepAlive(p)
		return int(done), nil
	}
	if !errors.Is(ioErr, windows.ERROR_IO_PENDING) {
		return 0, fmt.Errorf("named pipe I/O failed: %w", ioErr)
	}
	keep := true
	defer func() {
		if keep {
			_ = windows.CancelIoEx(c.handle, &overlapped)
			_ = windows.GetOverlappedResult(c.handle, &overlapped, &done, true)
		}
		runtime.KeepAlive(p)
		runtime.KeepAlive(&overlapped)
	}()
	for {
		wait, waitErr := windows.WaitForSingleObject(event, 100)
		if waitErr != nil {
			return 0, fmt.Errorf("wait for named pipe I/O: %w", waitErr)
		}
		if wait == windows.WAIT_OBJECT_0 {
			err := windows.GetOverlappedResult(c.handle, &overlapped, &done, false)
			keep = errors.Is(err, windows.ERROR_IO_INCOMPLETE)
			if err != nil {
				return 0, fmt.Errorf("complete named pipe I/O: %w", err)
			}
			return int(done), nil
		}
		if wait != uint32(windows.WAIT_TIMEOUT) {
			return 0, errors.New("unexpected named pipe wait result")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
	}
}

func connect(ctx context.Context, h windows.Handle) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return fmt.Errorf("create named pipe connect event: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(h, &overlapped)
	if err == nil || errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return fmt.Errorf("connect named pipe: %w", err)
	}
	keep := true
	defer func() {
		if keep {
			_ = windows.CancelIoEx(h, &overlapped)
			var done uint32
			_ = windows.GetOverlappedResult(h, &overlapped, &done, true)
		}
		runtime.KeepAlive(&overlapped)
	}()
	for {
		wait, waitErr := windows.WaitForSingleObject(event, 100)
		if waitErr != nil {
			return fmt.Errorf("wait for named pipe connection: %w", waitErr)
		}
		if wait == windows.WAIT_OBJECT_0 {
			var done uint32
			err := windows.GetOverlappedResult(h, &overlapped, &done, false)
			keep = errors.Is(err, windows.ERROR_IO_INCOMPLETE)
			if err != nil {
				return fmt.Errorf("complete named pipe connection: %w", err)
			}
			return nil
		}
		if wait != uint32(windows.WAIT_TIMEOUT) {
			return errors.New("unexpected named pipe connection wait result")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil named pipe context")
	}
	return ctx.Err()
}

func validate(name, serverSID, clientSID string) (string, string, string, error) {
	const prefix = `\\.\pipe\airc-gateway-`
	if !strings.HasPrefix(name, prefix) {
		return "", "", "", errors.New("invalid named pipe name")
	}
	suffix := strings.TrimPrefix(name, prefix)
	if len(suffix) < 1 || len(suffix) > 64 {
		return "", "", "", errors.New("invalid named pipe name")
	}
	for _, r := range suffix {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return "", "", "", errors.New("invalid named pipe name")
		}
	}
	owner, err := canonicalSID(serverSID)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid server SID: %w", err)
	}
	client, err := canonicalSID(clientSID)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid client SID: %w", err)
	}
	return name, owner, client, nil
}

func canonicalSID(value string) (string, error) {
	sid, err := windows.StringToSid(value)
	if err != nil || sid.String() != value {
		return "", errors.New("SID must be canonical numeric form")
	}
	return value, nil
}

func descriptor(owner, client string) (*windows.SECURITY_DESCRIPTOR, error) {
	sddl := fmt.Sprintf("O:%sD:P(A;;FA;;;%s)(A;;0x00120183;;;%s)", owner, owner, client)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("build named pipe security descriptor: %w", err)
	}
	return sd, nil
}

func verifyDescriptor(h windows.Handle, owner, client string) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect named pipe security descriptor: %w", err)
	}
	want, err := descriptor(owner, client)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("named pipe DACL is not protected")
	}
	gotSDDL, wantSDDL := sd.String(), want.String()
	if gotSDDL == "" || wantSDDL == "" || gotSDDL != wantSDDL {
		return errors.New("named pipe security descriptor does not match")
	}
	return nil
}

func requireCurrentSID(expected string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("inspect process SID: %w", err)
	}
	if user.User.Sid.String() != expected {
		return errors.New("process SID does not match named pipe role")
	}
	return nil
}
