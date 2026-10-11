package service

import (
	"context"
	"errors"
	"os"
	"runtime"

	"github.com/Someblueman/airc/internal/gateway/ipcepoch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
)

// openSystemVIngress requires the store's exclusive journal lock. Only exclusive
// creation can register ownership; unknown or changed existing queues are refused.
func openSystemVIngress(ctx context.Context, journal *store.Store, listener string, key int32) (_ *sysvqueue.Queue, failure error) {
	if ctx == nil || journal == nil || key <= 0 {
		return nil, store.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !(runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" || runtime.GOOS == "linux" && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64")) {
		return nil, sysvqueue.ErrUnsupported
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	epoch, err := ipcepoch.Current()
	if err != nil {
		return nil, err
	}
	previous, err := journal.LoadSystemVQueueOwnership(ctx, listener)
	known := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if known && (previous.Key != key || previous.Platform != platform) {
		return nil, store.ErrConflict
	}
	q, err := sysvqueue.Create(key)
	if errors.Is(err, os.ErrExist) {
		if !known {
			return nil, errors.Join(store.ErrConflict, err)
		}
		identity := sysvqueue.Identity{ID: int(previous.ID), Key: previous.Key, Sequence: previous.Sequence, Changed: previous.Changed}
		recorded := ipcepoch.Epoch{BootID: previous.BootID, NamespaceDevice: previous.NamespaceDevice, NamespaceInode: previous.NamespaceInode}
		q, err = sysvqueue.Resume(identity, recorded)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, q.Close())
		}
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			failure = errors.Join(failure, q.Close())
		}
	}()
	current, err := ipcepoch.Current()
	if err != nil {
		return nil, err
	}
	if current != epoch {
		return nil, store.ErrConflict
	}
	identity := q.Identity()
	next := store.SystemVQueueOwnership{Listener: listener, Platform: platform, Key: key, ID: int64(identity.ID), Sequence: identity.Sequence, Changed: identity.Changed, BootID: epoch.BootID, NamespaceDevice: epoch.NamespaceDevice, NamespaceInode: epoch.NamespaceInode}
	var expected *store.SystemVQueueOwnership
	if known {
		expected = &previous
	}
	if err := journal.SaveSystemVQueueOwnership(ctx, next, expected); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transferred = true
	return q, nil
}
