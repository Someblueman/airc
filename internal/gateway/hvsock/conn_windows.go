//go:build windows

package hvsock

import (
	"io"
	"net"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type conn struct {
	mu                          sync.Mutex
	writeMu                     sync.Mutex
	h                           windows.Handle
	closed                      bool
	local, remote               sockaddrHV
	deadlineMu                  sync.RWMutex
	readDeadline, writeDeadline time.Time
}

func newConn(h windows.Handle, local, remote sockaddrHV) *conn {
	return &conn{h: h, local: local, remote: remote}
}
func (c *conn) Network() string            { return "hvsock" }
func (c *conn) Read(p []byte) (int, error) { return c.readWrite(p, false) }
func (c *conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) != 0 {
		n, err := c.readWrite(p, true)
		total += n
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}
func (c *conn) readWrite(p []byte, writing bool) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		c.deadlineMu.RLock()
		deadline := c.readDeadline
		if writing {
			deadline = c.writeDeadline
		}
		c.deadlineMu.RUnlock()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, timeoutError{}
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return 0, net.ErrClosed
		}
		length := len(p)
		if uint64(length) > uint64(^uint32(0)) {
			length = int(^uint32(0))
		}
		buf := windows.WSABuf{Buf: &p[0], Len: uint32(length)}
		var n uint32
		var err error
		if writing {
			err = windows.WSASend(c.h, &buf, 1, &n, 0, nil, nil)
		} else {
			var flags uint32
			err = windows.WSARecv(c.h, &buf, 1, &n, &flags, nil, nil)
		}
		c.mu.Unlock()
		runtime.KeepAlive(p)
		if err == nil {
			if !writing && n == 0 {
				return 0, io.EOF
			}
			if n == 0 {
				return 0, io.ErrNoProgress
			}
			return int(n), nil
		}
		if !isWouldBlock(err) {
			return 0, err
		}
		if err = c.waitDeadline(writing); err != nil {
			return 0, err
		}
	}
}

func (c *conn) waitDeadline(writing bool) error {
	for {
		c.deadlineMu.RLock()
		deadline := c.readDeadline
		if writing {
			deadline = c.writeDeadline
		}
		c.deadlineMu.RUnlock()
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
		<-t.C
		t.Stop()
		// Re-read the deadline: SetDeadline can extend or shorten an active wait.
		if deadline.IsZero() || time.Now().Before(deadline) {
			return nil
		}
	}
}
func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return windows.Closesocket(c.h)
}
func (c *conn) CloseWrite() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	return windows.Shutdown(c.h, shutdownSend)
}
func (c *conn) LocalAddr() net.Addr  { return hvAddr(c.local) }
func (c *conn) RemoteAddr() net.Addr { return hvAddr(c.remote) }
func (c *conn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.deadlineMu.Unlock()
	return nil
}
func (c *conn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	c.deadlineMu.Unlock()
	return nil
}
func (c *conn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.writeDeadline = t
	c.deadlineMu.Unlock()
	return nil
}
