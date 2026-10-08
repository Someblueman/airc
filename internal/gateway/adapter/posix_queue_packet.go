package adapter

import (
	"context"
	"errors"
	"net"
)

// POSIXQueueAddr is a configured queue name, never producer identity.
type POSIXQueueAddr struct{ Name string }

func (*POSIXQueueAddr) Network() string  { return "posix_mqueue" }
func (a *POSIXQueueAddr) String() string { return a.Name }

// NewPOSIXQueuePackets transfers whole AGD1 fragments to fixed queue descriptors.
// Successful construction transfers all supplied handles to the packet owner.
func NewPOSIXQueuePackets(ingress *POSIXQueue, responses map[string]*POSIXQueue) (*queuePackets, error) {
	if ingress == nil || !ValidPOSIXQueueName(ingress.Name()) || len(responses) == 0 {
		return nil, ErrPOSIXQueueInvalid
	}
	routes := make(map[string]*POSIXQueue, len(responses))
	for name, q := range responses {
		if q == nil || !ValidPOSIXQueueName(name) || q.Name() != name || name == ingress.Name() {
			return nil, ErrPOSIXQueueInvalid
		}
		routes[name] = q
	}
	return &queuePackets{
		local: &POSIXQueueAddr{Name: ingress.Name()},
		read: func(ctx context.Context, body []byte) (int, error) {
			n, _, err := ingress.ReadMessage(ctx, body)
			return n, err
		},
		write: func(ctx context.Context, body []byte, dest net.Addr) error {
			addr, ok := dest.(*POSIXQueueAddr)
			if !ok || addr == nil || routes[addr.Name] == nil {
				return ErrPOSIXQueueInvalid
			}
			return routes[addr.Name].WriteMessage(ctx, body)
		},
		closeQueues: func() error {
			err := ingress.Close()
			for _, q := range routes {
				err = errors.Join(err, q.Close())
			}
			return err
		},
		operations: make(map[*queuePacketOperation]struct{}),
	}, nil
}
