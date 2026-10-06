package adapter

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// POSIXQueueAddr is a configured queue name, never producer identity.
type POSIXQueueAddr struct{ Name string }

func (*POSIXQueueAddr) Network() string  { return "posix_mqueue" }
func (a *POSIXQueueAddr) String() string { return a.Name }

type queuePacketOperation struct {
	write  bool
	cancel context.CancelFunc
}

// POSIXQueuePackets transfers whole AGD1 fragments to fixed queue descriptors.
// After successful construction it owns all supplied handles. Queue unlink
// ownership remains determined by exclusive creation versus opening a handle.
type POSIXQueuePackets struct {
	ingress                     *POSIXQueue
	responses                   map[string]*POSIXQueue
	mu                          sync.Mutex
	closed                      bool
	readDeadline, writeDeadline time.Time
	operations                  map[*queuePacketOperation]struct{}
	workers                     sync.WaitGroup
	once                        sync.Once
	closeErr                    error
}

var _ net.PacketConn = (*POSIXQueuePackets)(nil)

func NewPOSIXQueuePackets(ingress *POSIXQueue, responses map[string]*POSIXQueue) (*POSIXQueuePackets, error) {
	if ingress == nil || !ValidPOSIXQueueName(ingress.Name()) || len(responses) == 0 {
		return nil, ErrPOSIXQueueInvalid
	}
	copy := make(map[string]*POSIXQueue, len(responses))
	for name, queue := range responses {
		if queue == nil || !ValidPOSIXQueueName(name) || queue.Name() != name || name == ingress.Name() {
			return nil, ErrPOSIXQueueInvalid
		}
		copy[name] = queue
	}
	return &POSIXQueuePackets{ingress: ingress, responses: copy, operations: make(map[*queuePacketOperation]struct{})}, nil
}

func (c *POSIXQueuePackets) LocalAddr() net.Addr { return &POSIXQueueAddr{Name: c.ingress.Name()} }
func (c *POSIXQueuePackets) ReadFrom(body []byte) (int, net.Addr, error) {
	var n int
	err := c.operate(false, func(ctx context.Context) error {
		var err error
		n, _, err = c.ingress.ReadMessage(ctx, body)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	return n, c.LocalAddr(), nil
}
func (c *POSIXQueuePackets) WriteTo(body []byte, destination net.Addr) (int, error) {
	addr, ok := destination.(*POSIXQueueAddr)
	if !ok || addr == nil {
		return 0, ErrPOSIXQueueInvalid
	}
	queue := c.responses[addr.Name]
	if queue == nil {
		return 0, ErrPOSIXQueueInvalid
	}
	if err := c.operate(true, func(ctx context.Context) error { return queue.WriteMessage(ctx, body) }); err != nil {
		return 0, err
	}
	return len(body), nil
}

func (c *POSIXQueuePackets) operate(write bool, io func(context.Context) error) error {
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

func (c *POSIXQueuePackets) SetDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, true, true)
}
func (c *POSIXQueuePackets) SetReadDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, true, false)
}
func (c *POSIXQueuePackets) SetWriteDeadline(deadline time.Time) error {
	return c.setDeadline(deadline, false, true)
}
func (c *POSIXQueuePackets) setDeadline(deadline time.Time, read, write bool) error {
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
func (c *POSIXQueuePackets) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		for operation := range c.operations {
			operation.cancel()
		}
		c.mu.Unlock()
		c.closeErr = c.ingress.Close()
		for _, queue := range c.responses {
			c.closeErr = errors.Join(c.closeErr, queue.Close())
		}
		c.workers.Wait()
	})
	return c.closeErr
}
