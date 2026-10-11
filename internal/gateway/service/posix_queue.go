package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func newPOSIXQueueOwner(ctx context.Context, c config.Config, l config.Listener, journal *store.Store, d *dispatch.Dispatcher, slots, requests chan struct{}) (_ *packetOwner, failure error) {
	driver, replies, err := newLocalPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	responses := make(map[string]*adapter.POSIXQueue)
	var ingress *adapter.POSIXQueue
	transferred := false
	defer func() {
		if transferred {
			return
		}
		for _, queue := range responses {
			failure = errors.Join(failure, queue.Close())
		}
		if ingress != nil {
			failure = errors.Join(failure, ingress.Close())
		}
	}()
	// Provisioned response queues are verified before creating an owned ingress.
	// Closing these opened handles never unlinks their externally owned names.
	for _, reply := range replies {
		addr, ok := reply.(*adapter.POSIXQueueAddr)
		if !ok || l.POSIXQueue == nil {
			return nil, store.ErrInvalid
		}
		if responses[addr.Name] != nil {
			continue
		}
		queue, err := adapter.OpenPOSIXQueue(addr.Name, l.POSIXQueue.Depth)
		if err != nil {
			return nil, err
		}
		responses[addr.Name] = queue
	}
	ingress, err = openPOSIXIngress(ctx, journal, l)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.NewPOSIXQueuePackets(ingress, responses)
	if err != nil {
		return nil, err
	}
	transferred = true
	return &packetOwner{conn: conn, driver: driver}, nil
}
