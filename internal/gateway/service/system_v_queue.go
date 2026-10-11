package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
)

func newSystemVQueueOwner(ctx context.Context, c config.Config, l config.Listener, journal *store.Store, d *dispatch.Dispatcher, slots, requests chan struct{}) (_ *packetOwner, failure error) {
	key, err := config.SystemVQueueKey(l.Address)
	if err != nil {
		return nil, err
	}
	driver, replies, err := newLocalPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	responses := make(map[int32]*sysvqueue.Queue)
	var ingress *sysvqueue.Queue
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
	// Closing these opened handles never removes their externally owned queues.
	for _, reply := range replies {
		addr, ok := reply.(*adapter.SystemVQueueAddr)
		if !ok {
			return nil, store.ErrInvalid
		}
		if responses[addr.Key] != nil {
			continue
		}
		queue, err := sysvqueue.Open(addr.Key)
		if err != nil {
			return nil, err
		}
		responses[addr.Key] = queue
	}
	ingress, err = openSystemVIngress(ctx, journal, l.ID, key)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.NewSystemVQueuePackets(ingress, responses)
	if err != nil {
		return nil, err
	}
	transferred = true
	return &packetOwner{conn: conn, driver: driver}, nil
}
