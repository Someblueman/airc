package adapter

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

type queuePacketOperation struct {
	write  bool
	cancel context.CancelFunc
}

// queuePackets owns verified queue handles and joins packet operations on Close.
// Sources identify only the configured ingress, never authenticated producers.
type queuePackets struct {
	local                       net.Addr
	read                        func(context.Context, []byte) (int, error)
	write                       func(context.Context, []byte, net.Addr) error
	closeQueues                 func() error
	mu                          sync.Mutex
	closed                      bool
	readDeadline, writeDeadline time.Time
	operations                  map[*queuePacketOperation]struct{}
	workers                     sync.WaitGroup
	once                        sync.Once
	closeErr                    error
}

var _ net.PacketConn = (*queuePackets)(nil)

func (c *queuePackets) LocalAddr() net.Addr { return clonePacketAddr(c.local) }
func (c *queuePackets) ReadFrom(body []byte) (int, net.Addr, error) {
	var n int
	err := c.operate(false, func(ctx context.Context) error {
		var err error
		n, err = c.read(ctx, body)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	return n, c.LocalAddr(), nil
}
func (c *queuePackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	if err := c.operate(true, func(ctx context.Context) error { return c.write(ctx, body, destination) }); err != nil {
		return 0, err
	}
	return len(body), nil
}
func (c *queuePackets) operate(write bool, io func(context.Context) error) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return net.ErrClosed
		}
		deadline := c.readDeadline
		if write {
			deadline = c.writeDeadline
		}
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline.IsZero() {
			ctx, cancel = context.WithCancel(context.Background())
		} else {
			ctx, cancel = context.WithDeadline(context.Background(), deadline)
		}
		operation := &queuePacketOperation{write: write, cancel: cancel}
		c.operations[operation] = struct{}{}
		c.mu.Unlock()
		err := io(ctx)
		cancel()
		c.mu.Lock()
		delete(c.operations, operation)
		closed := c.closed
		currentDeadline := c.readDeadline
		if write {
			currentDeadline = c.writeDeadline
		}
		c.mu.Unlock()
		if err == nil {
			return nil
		}
		if closed {
			return net.ErrClosed
		}
		if errors.Is(err, context.Canceled) {
			continue
		} // Deadline was changed; retry against its new value.
		if errors.Is(err, context.DeadlineExceeded) {
			if currentDeadline != deadline {
				continue
			}
			return os.ErrDeadlineExceeded
		}
		return err
	}
}

func (c *queuePackets) SetDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, true, true)
}
func (c *queuePackets) SetReadDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, true, false)
}
func (c *queuePackets) SetWriteDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, false, true)
}
func (c *queuePackets) setDeadline(deadline time.Time, read, write bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if read {
		c.readDeadline = deadline
	}
	if write {
		c.writeDeadline = deadline
	}
	for operation := range c.operations {
		if operation.write && write || !operation.write && read {
			operation.cancel()
		}
	}
	return nil
}
func (c *queuePackets) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		for operation := range c.operations {
			operation.cancel()
		}
		c.mu.Unlock()
		c.closeErr = c.closeQueues()
		c.workers.Wait()
	})
	return c.closeErr
}
