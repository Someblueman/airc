package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// newMappedFileOwner opens only pre-provisioned queue directories. The mapper
// retains and validates the queue and lock descriptors; this owner does not
// create, initialize, resize or unlink queue resources.
func newMappedFileOwner(ctx context.Context, c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (_ *packetOwner, failure error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Protocol != "mapped_file" || !config.Available("mapped_file") {
		return nil, store.ErrInvalid
	}
	driver, replies, err := newLocalPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	opened := make(map[string]*adapter.MappedQueue, len(replies)+1)
	transferred := false
	defer func() {
		if transferred {
			return
		}
		for _, queue := range opened {
			failure = errors.Join(failure, queue.Close())
		}
	}()
	open := func(directory string, consumer bool) (*adapter.MappedQueue, error) {
		if !adapter.ValidMappedFileDirectory(directory) || opened[directory] != nil {
			return nil, store.ErrInvalid
		}
		queue, err := adapter.OpenMappedFileQueue(directory, consumer)
		if err != nil {
			return nil, err
		}
		opened[directory] = queue
		return queue, nil
	}
	responses := make(map[string]*adapter.MappedQueue, len(replies))
	for _, reply := range replies {
		addr, ok := reply.(*adapter.MappedFileAddr)
		if !ok || addr == nil || responses[addr.Directory] != nil {
			return nil, store.ErrInvalid
		}
		queue, err := open(addr.Directory, false)
		if err != nil {
			return nil, err
		}
		responses[addr.Directory] = queue
	}
	ingress, err := open(l.Address, true)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.NewMappedFilePackets(ingress, responses)
	if err != nil {
		return nil, err
	}
	transferred = true
	return &packetOwner{conn: conn, driver: driver}, nil
}
