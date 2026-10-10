//go:build darwin || linux

package adapter

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// uartConn owns one tty descriptor and restores the tty's original settings on
// Close. The caller owns the authenticated protocol and session lifecycle.
type uartConn struct {
	file      *os.File
	fd        int
	path      string
	original  unix.Termios
	closeOnce sync.Once
	closeErr  error
}

// OpenUART opens a canonical, absolute tty path, locks out other gateway openers,
// and configures fixed 115200 baud, 8N1, raw mode, with no flow control.
func OpenUART(path string) (_ net.Conn, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("tty path must be canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve tty path: %w", err)
	}
	if resolved != path {
		return nil, fmt.Errorf("tty path must not contain symlinks")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat tty path: %w", err)
	}
	if pathInfo.Mode()&os.ModeCharDevice == 0 || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("tty path is not a character device")
	}

	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open tty: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	failed := true
	claimed := false
	configured := false
	var original unix.Termios
	defer func() {
		if failed {
			var cleanupErr error
			if configured {
				cleanupErr = errors.Join(cleanupErr, uartSetTermios(fd, original))
			}
			if claimed {
				cleanupErr = errors.Join(cleanupErr, uartReleaseTTY(fd))
			}
			cleanupErr = errors.Join(cleanupErr, file.Close())
			err = errors.Join(err, cleanupErr)
		}
	}()

	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened tty: %w", err)
	}
	if openedInfo.Mode()&os.ModeCharDevice == 0 || !os.SameFile(pathInfo, openedInfo) {
		return nil, fmt.Errorf("tty path changed or is not a character device")
	}

	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("claim tty lock: %w", err)
	}
	// Snapshot settings only after ownership; a previous owner may be
	// restoring them as this opener arrives.
	original, err = uartGetTermios(fd)
	if err != nil {
		return nil, fmt.Errorf("verify tty descriptor: %w", err)
	}
	if err := uartClaimTTY(fd); err != nil {
		return nil, fmt.Errorf("claim tty exclusively: %w", err)
	}
	claimed = true
	raw := uartRaw115200(original)
	configured = true
	if err := uartSetTermios(fd, raw); err != nil {
		return nil, fmt.Errorf("configure tty: %w", err)
	}
	deadline := time.Now().Add(time.Second)
	if err := file.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("verify tty read deadlines: %w", err)
	}
	if err := file.SetWriteDeadline(deadline); err != nil {
		return nil, fmt.Errorf("verify tty write deadlines: %w", err)
	}
	if err := file.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("clear tty startup deadlines: %w", err)
	}
	failed = false
	return &uartConn{file: file, fd: fd, path: path, original: original}, nil
}

func (c *uartConn) Read(p []byte) (int, error)         { return c.file.Read(p) }
func (c *uartConn) Write(p []byte) (int, error)        { return c.file.Write(p) }
func (c *uartConn) SetDeadline(t time.Time) error      { return c.file.SetDeadline(t) }
func (c *uartConn) SetReadDeadline(t time.Time) error  { return c.file.SetReadDeadline(t) }
func (c *uartConn) SetWriteDeadline(t time.Time) error { return c.file.SetWriteDeadline(t) }

func (c *uartConn) Close() error {
	c.closeOnce.Do(func() {
		// Restore settings and explicitly release tty exclusivity before close;
		// closing the descriptor releases the advisory flock atomically.
		c.closeErr = errors.Join(uartSetTermios(c.fd, c.original), uartReleaseTTY(c.fd), c.file.Close())
	})
	return c.closeErr
}

func (c *uartConn) LocalAddr() net.Addr  { return uartAddr(c.path) }
func (c *uartConn) RemoteAddr() net.Addr { return uartAddr("peer") }

type uartAddr string

func (a uartAddr) Network() string { return "uart" }
func (a uartAddr) String() string  { return string(a) }

var _ net.Conn = (*uartConn)(nil)
