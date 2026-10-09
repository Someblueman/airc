package adapter

import (
	"context"
	"errors"
	"net"
)

// SharedMemoryAddr is an owner-configured queue name, not a producer identity.
type SharedMemoryAddr struct{ Name string }

func (*SharedMemoryAddr) Network() string { return "shared_memory" }
func (a *SharedMemoryAddr) String() string {
	if a == nil {
		return ""
	}
	return a.Name
}

// NewSharedMemoryPackets transfers ingress and fixed response queue handles to
// queuePackets. The low-level queue owns AGD1 whole-fragment send/receive.
func NewSharedMemoryPackets(ingress *SharedMemoryQueue, responses map[string]*SharedMemoryQueue) (*queuePackets, error) {
	if ingress == nil || !ingress.consumer || !ValidSharedMemoryName(ingress.Name()) || len(responses) == 0 {
		return nil, ErrSharedMemoryInvalid
	}
	routes := make(map[string]*SharedMemoryQueue, len(responses))
	for name, queue := range responses {
		if queue == nil || queue.consumer || !ValidSharedMemoryName(name) || queue.Name() != name || name == ingress.Name() {
			return nil, ErrSharedMemoryInvalid
		}
		routes[name] = queue
	}
	return &queuePackets{
		local: &SharedMemoryAddr{Name: ingress.Name()},
		read: func(ctx context.Context, body []byte) (int, error) {
			return ingress.ReadMessage(ctx, body)
		},
		write: func(ctx context.Context, body []byte, destination net.Addr) error {
			addr, ok := destination.(*SharedMemoryAddr)
			if !ok || addr == nil || routes[addr.Name] == nil {
				return ErrSharedMemoryInvalid
			}
			return routes[addr.Name].WriteMessage(ctx, body)
		},
		closeQueues: func() error {
			err := ingress.Close()
			for _, queue := range routes {
				err = errors.Join(err, queue.Close())
			}
			return err
		},
		operations: make(map[*queuePacketOperation]struct{}),
	}, nil
}
