package service

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// newSharedMemoryOwner opens only provisioned queue objects. The adapter checks
// their owner, permissions, size and lock layout; this owner never creates or
// unlinks an object. Open descriptors remain attached to the queue while used.
func newSharedMemoryOwner(ctx context.Context, c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (_ *packetOwner, failure error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Protocol != "shared_memory" || l.SharedMemory == nil || !config.SharedMemoryAvailable() {
		return nil, store.ErrInvalid
	}
	driver, replies, err := newLocalPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	root := l.SharedMemory.LockDirectory
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
	open := func(name string, consumer bool) (*adapter.MappedQueue, error) {
		if !adapter.ValidSharedMemoryName(name) {
			return nil, store.ErrInvalid
		}
		if queue := opened[name]; queue != nil {
			return nil, store.ErrInvalid
		}
		queue, err := adapter.OpenSharedMemoryQueue(name, filepath.Join(root, name[1:]), consumer)
		if err != nil {
			return nil, err
		}
		opened[name] = queue
		return queue, nil
	}

	responses := make(map[string]*adapter.MappedQueue, len(replies))
	for _, reply := range replies {
		addr, ok := reply.(*adapter.SharedMemoryAddr)
		if !ok || addr == nil {
			return nil, store.ErrInvalid
		}
		if responses[addr.Name] != nil {
			return nil, store.ErrInvalid
		}
		queue, err := open(addr.Name, false)
		if err != nil {
			return nil, err
		}
		responses[addr.Name] = queue
	}
	ingress, err := open(l.Address, true)
	if err != nil {
		return nil, err
	}
	conn, err := adapter.NewSharedMemoryPackets(ingress, responses)
	if err != nil {
		return nil, err
	}
	transferred = true
	return &packetOwner{conn: conn, driver: driver}, nil
}
