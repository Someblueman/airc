package adapter

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// amqpConnection owns the negotiated socket and joins cancellation and heartbeat
// work on close. The future listener owner must bound accepted connections and
// close every returned connection; this type does not authorize requests.
type amqpConnection struct {
	conn          net.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	stop          func() bool
	closed        chan struct{}
	heartbeatDone chan struct{}
	writeMu       sync.Mutex
	closeOnce     sync.Once
	identity      auth.Identity
	negotiation   amqpwire.Negotiation
}

func openAMQPConnection(parent context.Context, conn net.Conn, v *auth.Verifier, routes []config.AMQPRoute, container string) (*amqpConnection, error) {
	if conn == nil {
		return nil, auth.ErrAuthentication
	}
	ctx, cancel := context.WithCancel(parent)
	c := &amqpConnection{conn: conn, ctx: ctx, cancel: cancel, closed: make(chan struct{})}
	c.stop = context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(c.closed) })
	identity, err := authenticateAMQP(ctx, conn, v, routes)
	if err != nil {
		c.close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(amqpDeadline)); err != nil {
		c.close()
		return nil, err
	}
	n, err := amqpwire.NegotiateSession(conn, conn, container)
	if err != nil {
		c.close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		c.close()
		return nil, err
	}
	c.identity, c.negotiation = identity, n
	c.heartbeatDone = make(chan struct{})
	go c.heartbeats()
	return c, nil
}

func (c *amqpConnection) close() {
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

// read has one owner. A deadline covers the entire frame, so partial-frame
// timeouts terminate the connection rather than treating residual bytes as a
// new frame. Incoming frame/channel bounds are the limits we advertised.
func (c *amqpConnection) read() (amqpwire.Performative, error) {
	if err := c.ctx.Err(); err != nil {
		return amqpwire.Performative{}, err
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return amqpwire.Performative{}, err
	}
	f, err := amqpwire.ReadFrame(c.conn, amqpwire.MaxFrameBytes)
	if err != nil {
		return amqpwire.Performative{}, err
	}
	if f.Channel != 0 {
		return amqpwire.Performative{}, amqpwire.ErrNegotiation
	}
	return amqpwire.DecodePerformative(f)
}

func (c *amqpConnection) write(p amqpwire.Performative) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return err
	}
	f, err := amqpwire.EncodePerformative(p, 0, c.negotiation.PeerFrameLimit)
	if err != nil {
		return err
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(amqpDeadline)); err != nil {
		return err
	}
	return amqpwire.WriteFrame(c.conn, f, c.negotiation.PeerFrameLimit)
}

func (c *amqpConnection) heartbeats() {
	defer close(c.heartbeatDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if c.write(amqpwire.Performative{}) != nil {
				c.cancel()
				return
			}
		}
	}
}
