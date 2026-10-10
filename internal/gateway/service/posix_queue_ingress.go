package service

import (
	"context"
	"errors"
	"os"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// openPOSIXIngress requires the store's exclusive journal lock. The listener's
// name/depth remain pinned to its record; an unknown existing name is refused.
func openPOSIXIngress(ctx context.Context, journal *store.Store, listener config.Listener) (*adapter.POSIXQueue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if journal == nil || listener.Protocol != "posix_mqueue" || listener.POSIXQueue == nil || !adapter.ValidPOSIXQueueName(listener.Address) || listener.POSIXQueue.Depth < 1 || listener.POSIXQueue.Depth > adapter.POSIXQueueMaxDepth {
		return nil, store.ErrInvalid
	}
	epoch, err := adapter.CurrentPOSIXQueueEpoch()
	if err != nil {
		return nil, err
	}
	previous, err := journal.LoadPOSIXQueueOwnership(ctx, listener.ID)
	known := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if known && (previous.Name != listener.Address || previous.Depth != listener.POSIXQueue.Depth) {
		return nil, store.ErrConflict
	}
	queue, err := adapter.CreatePOSIXQueue(listener.Address, listener.POSIXQueue.Depth)
	if errors.Is(err, os.ErrExist) {
		if !known {
			return nil, errors.Join(store.ErrConflict, err)
		}
		recordedEpoch := adapter.POSIXQueueEpoch{BootID: previous.BootID, NamespaceDevice: previous.NamespaceDevice, NamespaceInode: previous.NamespaceInode}
		identity := adapter.POSIXQueueIdentity{Device: previous.QueueDevice, Inode: previous.QueueInode}
		return adapter.ResumePOSIXQueue(listener.Address, listener.POSIXQueue.Depth, identity, recordedEpoch)
	}
	if err != nil {
		return nil, err
	}
	identity := queue.Identity()
	next := store.POSIXQueueOwnership{Listener: listener.ID, Name: listener.Address, Depth: listener.POSIXQueue.Depth, BootID: epoch.BootID, NamespaceDevice: epoch.NamespaceDevice, NamespaceInode: epoch.NamespaceInode, QueueDevice: identity.Device, QueueInode: identity.Inode}
	var expected *store.POSIXQueueOwnership
	if known {
		expected = &previous
	}
	if err := journal.SavePOSIXQueueOwnership(ctx, next, expected); err != nil {
		return nil, errors.Join(err, queue.Close())
	}
	return queue, nil
}
