package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

const natsHeartbeatInterval = 10 * time.Second

// natsConnection owns the upgraded socket, buffered reader and joined heartbeat
// lifetime. Its caller owns principal admission and application dispatch.
type natsConnection struct {
	conn          net.Conn
	reader        *bufio.Reader
	ctx           context.Context
	cancel        context.CancelFunc
	stop          func() bool
	closed        chan struct{}
	heartbeatDone chan struct{}
	identity      auth.Identity
	verbose       bool
	routes        []config.NATSRoute
	subscription  *natsSubscription
	writeMu       sync.Mutex
	closing       bool
	started       bool
	outstanding   int
	closeOnce     sync.Once
}

func openNATSConnection(parent context.Context, conn net.Conn, security *tls.Config, v *auth.Verifier, routes []config.NATSRoute, info natsInfo) (*natsConnection, error) {
	if conn == nil {
		return nil, auth.ErrAuthentication
	}
	ctx, cancel := context.WithCancel(parent)
	c := &natsConnection{conn: conn, ctx: ctx, cancel: cancel, closed: make(chan struct{}), heartbeatDone: make(chan struct{})}
	c.stop = context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(c.closed) })
	s, err := authenticateNATS(ctx, conn, security, v, routes, info)
	if err != nil {
		c.close()
		return nil, err
	}
	c.conn, c.reader, c.identity, c.verbose = s.conn, s.reader, s.identity, s.verbose
	for _, r := range routes {
		if r.Principal == c.identity.ID() {
			r.Operations = slices.Clone(r.Operations)
			c.routes = append(c.routes, r)
		}
	}
	return c, nil
}

// connected starts heartbeats only after the listener's principal admission.
func (c *natsConnection) connected() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.started || c.closing {
		return natswire.ErrFrame
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if c.verbose {
		if err := c.writeLocked(natswire.Frame{Command: "+OK"}, time.Time{}, false); err != nil {
			return err
		}
	}
	c.started = true
	go c.heartbeats()
	return nil
}

func (c *natsConnection) close() {
	c.closeOnce.Do(func() {
		c.cancel()
		if !c.stop() {
			<-c.closed
		}
		closeMQTTConnection(c.conn)
		c.writeMu.Lock()
		started := c.started
		c.writeMu.Unlock()
		if started {
			<-c.heartbeatDone
		}
	})
}

func (c *natsConnection) readBefore(operationDeadline time.Time) (natswire.Frame, error) {
	if err := c.ctx.Err(); err != nil {
		return natswire.Frame{}, err
	}
	deadline := stompEarlier(time.Now().Add(30*time.Second), operationDeadline)
	if !time.Now().Before(deadline) {
		return natswire.Frame{}, context.DeadlineExceeded
	}
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return natswire.Frame{}, err
	}
	if _, err := c.reader.Peek(1); err != nil {
		return natswire.Frame{}, err
	}
	frameDeadline := stompEarlier(time.Now().Add(natsDeadline), deadline)
	if err := c.conn.SetReadDeadline(frameDeadline); err != nil {
		return natswire.Frame{}, err
	}
	f, err := natswire.Read(c.reader)
	if err == nil && !time.Now().Before(frameDeadline) {
		return natswire.Frame{}, context.DeadlineExceeded
	}
	return f, err
}

func (c *natsConnection) writeBefore(f natswire.Frame, deadline time.Time, terminal bool) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeLocked(f, deadline, terminal)
}

func (c *natsConnection) writeLocked(f natswire.Frame, deadline time.Time, terminal bool) error {
	if c.closing {
		return natswire.ErrFrame
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	wire, err := natswire.Encode(f)
	if err != nil {
		return err
	}
	if terminal || strings.EqualFold(f.Command, "-ERR") {
		c.closing = true
	}
	if err := c.conn.SetWriteDeadline(stompEarlier(time.Now().Add(natsDeadline), deadline)); err != nil {
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

func (c *natsConnection) ping() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.outstanding >= 2 {
		return natswire.ErrFrame
	}
	if err := c.writeLocked(natswire.Frame{Command: "PING"}, time.Time{}, false); err != nil {
		return err
	}
	c.outstanding++
	return nil
}

func (c *natsConnection) pong() { c.writeMu.Lock(); c.outstanding = 0; c.writeMu.Unlock() }

func (c *natsConnection) heartbeats() {
	defer close(c.heartbeatDone)
	ticker := time.NewTicker(natsHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if c.ping() != nil {
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

func (c *natsConnection) fail() {
	_ = c.writeBefore(natswire.Frame{Command: "-ERR", Error: "NATS request refused"}, time.Time{}, true)
}
