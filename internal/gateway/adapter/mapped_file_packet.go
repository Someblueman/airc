package adapter

import (
	"context"
	"errors"
	"net"
)

// MappedFileAddr is a fixed private queue directory, not a producer identity.
type MappedFileAddr struct{ Directory string }

func (*MappedFileAddr) Network() string { return "mapped_file" }
func (a *MappedFileAddr) String() string {
	if a == nil {
		return ""
	}
	return a.Directory
}

// NewMappedFilePackets transfers ingress and fixed response queue handles to
// queuePackets. The mapped queue carries whole protected AGD1 fragments.
func NewMappedFilePackets(ingress *MappedQueue, responses map[string]*MappedQueue) (*queuePackets, error) {
	if ingress == nil || !ingress.fileBacked || !ingress.consumer || !ValidMappedFileDirectory(ingress.Name()) || len(responses) == 0 {
		return nil, ErrMappedQueueInvalid
	}
	routes := make(map[string]*MappedQueue, len(responses))
	for directory, queue := range responses {
		if queue == nil || !queue.fileBacked || queue.consumer || !ValidMappedFileDirectory(directory) || queue.Name() != directory || directory == ingress.Name() {
			return nil, ErrMappedQueueInvalid
		}
		routes[directory] = queue
	}
	return &queuePackets{
		local: &MappedFileAddr{Directory: ingress.Name()},
		read: func(ctx context.Context, body []byte) (int, error) {
			return ingress.ReadMessage(ctx, body)
		},
		write: func(ctx context.Context, body []byte, destination net.Addr) error {
			addr, ok := destination.(*MappedFileAddr)
			if !ok || addr == nil || routes[addr.Directory] == nil {
				return ErrMappedQueueInvalid
			}
			return routes[addr.Directory].WriteMessage(ctx, body)
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
