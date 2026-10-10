//go:build linux

package bluezgatt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxBusMessageBytes = 64 << 10
	busFrameTimeout    = 5 * time.Second
)

// boundedBusConn validates each D-Bus frame's declared size before exposing it
// to godbus. Before binaryMode is enabled it passes the authentication stream
// through unchanged.
type boundedBusConn struct {
	conn            net.Conn
	binary          atomic.Bool
	startup         atomic.Bool
	startupMu       sync.RWMutex
	startupDeadline time.Time
	readFrameActive bool
	authReadBytes   int
	authWriteBytes  int
	readMu          sync.Mutex
	frame           []byte
	frameAt         int
	closed          atomic.Bool
	writeMu         sync.Mutex
}

func (c *boundedBusConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if !c.binary.Load() {
		remaining := 4097 - c.authReadBytes
		if remaining <= 0 {
			return 0, errors.New("D-Bus authentication stream exceeds size limit")
		}
		if len(p) > remaining {
			p = p[:remaining]
		}
		n, err := c.conn.Read(p)
		c.authReadBytes += n
		if c.authReadBytes > 4096 {
			return 0, errors.New("D-Bus authentication stream exceeds size limit")
		}
		return n, err
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.frameAt >= len(c.frame) {
		frame, err := c.readFrame()
		if err != nil {
			return 0, err
		}
		c.frame = frame
		c.frameAt = 0
	}
	n := copy(p, c.frame[c.frameAt:])
	c.frameAt += n
	if c.frameAt == len(c.frame) {
		clear(c.frame)
		c.frame = nil
		c.frameAt = 0
	}
	return n, nil
}

func (c *boundedBusConn) readFrame() ([]byte, error) {
	c.startupMu.Lock()
	firstByteDeadline := time.Time{}
	if c.startup.Load() {
		firstByteDeadline = c.startupDeadline
	}
	if err := c.conn.SetReadDeadline(firstByteDeadline); err != nil {
		c.startupMu.Unlock()
		return nil, err
	}
	c.startupMu.Unlock()
	var first [1]byte
	if _, err := io.ReadFull(c.conn, first[:]); err != nil {
		return nil, err
	}
	continuationDeadline := time.Now().Add(busFrameTimeout)
	c.startupMu.Lock()
	if c.startup.Load() && c.startupDeadline.Before(continuationDeadline) {
		continuationDeadline = c.startupDeadline
	}
	c.readFrameActive = true
	if err := c.conn.SetReadDeadline(continuationDeadline); err != nil {
		c.readFrameActive = false
		c.startupMu.Unlock()
		return nil, err
	}
	c.startupMu.Unlock()
	var header [16]byte
	header[0] = first[0]
	if _, err := io.ReadFull(c.conn, header[1:]); err != nil {
		return nil, err
	}
	var order binary.ByteOrder
	switch header[0] {
	case 'l':
		order = binary.LittleEndian
	case 'B':
		order = binary.BigEndian
	default:
		return nil, errors.New("invalid D-Bus byte order")
	}
	if header[1] < 1 || header[1] > 4 || header[3] != 1 {
		return nil, errors.New("invalid D-Bus message header")
	}
	bodyLen := uint64(order.Uint32(header[4:8]))
	headerLen := uint64(order.Uint32(header[12:16]))
	if headerLen > maxBusMessageBytes-16 {
		return nil, errors.New("D-Bus header exceeds size limit")
	}
	bodyStart := uint64(16) + ((headerLen + 7) &^ uint64(7))
	total := bodyStart + bodyLen
	if total < bodyStart || total > maxBusMessageBytes {
		return nil, errors.New("D-Bus message exceeds size limit")
	}
	frame := make([]byte, int(total))
	copy(frame, header[:])
	if _, err := io.ReadFull(c.conn, frame[16:]); err != nil {
		clear(frame)
		c.resetFrameReadDeadline()
		return nil, err
	}
	if err := c.resetFrameReadDeadline(); err != nil {
		clear(frame)
		return nil, err
	}
	return frame, nil
}

func (c *boundedBusConn) setStartupDeadline(deadline time.Time) {
	c.startupMu.Lock()
	c.startupDeadline = deadline
	c.startup.Store(true)
	c.startupMu.Unlock()
}

func (c *boundedBusConn) finishStartup() error {
	c.startupMu.Lock()
	defer c.startupMu.Unlock()
	c.startup.Store(false)
	c.startupDeadline = time.Time{}
	if !c.readFrameActive {
		return c.conn.SetReadDeadline(time.Time{})
	}
	return nil
}

func (c *boundedBusConn) resetFrameReadDeadline() error {
	c.startupMu.Lock()
	defer c.startupMu.Unlock()
	deadline := time.Time{}
	if c.startup.Load() {
		deadline = c.startupDeadline
	}
	c.readFrameActive = false
	return c.conn.SetReadDeadline(deadline)
}

func (c *boundedBusConn) Write(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if !c.binary.Load() {
		c.authWriteBytes += len(p)
		if c.authWriteBytes > 4096 {
			return 0, errors.New("D-Bus authentication stream exceeds size limit")
		}
		n, err := c.conn.Write(p)
		if err == nil && n == len(p) && bytes.Equal(p, []byte("BEGIN\r\n")) {
			c.binary.Store(true)
		}
		return n, err
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(busFrameTimeout)); err != nil {
		return 0, err
	}
	defer c.conn.SetWriteDeadline(time.Time{})
	written := 0
	for written < len(p) {
		n, err := c.conn.Write(p[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}

func (c *boundedBusConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.conn.Close()
}
