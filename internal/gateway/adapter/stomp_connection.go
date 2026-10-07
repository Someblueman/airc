package adapter

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// stompConnection owns its socket, buffered input, cancellation and heartbeat
// writer. Listener admission and request authority belong to their own owners.
type stompConnection struct {
	conn          net.Conn
	reader        *bufio.Reader
	ctx           context.Context
	cancel        context.CancelFunc
	stop          func() bool
	closed        chan struct{}
	heartbeatDone chan struct{}
	identity      auth.Identity
	heartbeat     stompHeartbeat
	writeMu       sync.Mutex
	closing       bool
	closeOnce     sync.Once
}

func openSTOMPConnection(parent context.Context, conn net.Conn, v *auth.Verifier, routes []config.STOMPRoute, realm string) (*stompConnection, error) {
	if conn == nil {
		return nil, auth.ErrAuthentication
	}
	ctx, cancel := context.WithCancel(parent)
	c := &stompConnection{conn: conn, reader: bufio.NewReaderSize(conn, 4096), ctx: ctx, cancel: cancel, closed: make(chan struct{})}
	c.stop = context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(c.closed) })
	identity, heartbeat, err := authenticateSTOMP(ctx, conn, c.reader, v, routes, realm)
	if err != nil {
		c.close()
		return nil, err
	}
	c.identity, c.heartbeat = identity, heartbeat
	if err := c.write(stompwire.Frame{Command: "CONNECTED", Headers: []stompwire.Header{{Key: "version", Value: "1.2"}, {Key: "heart-beat", Value: "1000,1000"}}}); err != nil {
		c.close()
		return nil, err
	}
	c.heartbeatDone = make(chan struct{})
	go c.heartbeats()
	return c, nil
}

func (c *stompConnection) close() {
	c.closeOnce.Do(func() {
		c.cancel()
		if !c.stop() {
			<-c.closed
		}
		closeMQTTConnection(c.conn)
		if c.heartbeatDone != nil {
			<-c.heartbeatDone
		}
	})
}

// readBefore waits for the first byte under the negotiated idle bound, then
// applies a fixed three-second bound to the entire frame. Neither partial bytes
// nor heartbeat EOLs extend the caller's operation deadline.
func (c *stompConnection) readBefore(operationDeadline time.Time) (stompwire.Frame, error) {
	if err := c.ctx.Err(); err != nil {
		return stompwire.Frame{}, err
	}
	idle := 30 * time.Second
	if c.heartbeat.receive != 0 {
		idle = 2 * c.heartbeat.receive
	}
	deadline := stompEarlier(time.Now().Add(idle), operationDeadline)
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return stompwire.Frame{}, err
	}
	if _, err := c.reader.Peek(1); err != nil {
		return stompwire.Frame{}, err
	}
	if err := c.conn.SetReadDeadline(stompEarlier(time.Now().Add(stompDeadline), deadline)); err != nil {
		return stompwire.Frame{}, err
	}
	return stompwire.Read(c.reader)
}

func (c *stompConnection) write(f stompwire.Frame) error { return c.writeBefore(f, time.Time{}, false) }

// terminal serializes a final RECEIPT/ERROR with the heartbeat writer. Once
// claimed, no later heartbeat or application frame can be written.
func (c *stompConnection) writeBefore(f stompwire.Frame, deadline time.Time, terminal bool) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closing {
		return stompwire.ErrFrame
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	wire, err := stompwire.Encode(f)
	if err != nil {
		return err
	}
	if terminal || f.Command == "ERROR" {
		c.closing = true
	}
	if err := c.conn.SetWriteDeadline(stompEarlier(time.Now().Add(stompDeadline), deadline)); err != nil {
		return err
	}
	for len(wire) > 0 {
		n, err := c.conn.Write(wire)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		wire = wire[n:]
	}
	return nil
}

func (c *stompConnection) heartbeats() {
	defer close(c.heartbeatDone)
	if c.heartbeat.send == 0 {
		return
	}
	ticker := time.NewTicker(c.heartbeat.send)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if err := c.write(stompwire.Frame{}); err != nil {
				c.writeMu.Lock()
				closing := c.closing
				c.writeMu.Unlock()
				if !closing {
					c.cancel()
				}
				return
			}
		}
	}
}

func stompEarlier(a, b time.Time) time.Time {
	if !b.IsZero() && b.Before(a) {
		return b
	}
	return a
}
