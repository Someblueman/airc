//go:build linux && (arm64 || amd64)

package adapter

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/ipcepoch"
)

// CurrentPOSIXQueueEpoch scopes queue ownership to verified kernel metadata.
func CurrentPOSIXQueueEpoch() (POSIXQueueEpoch, error) {
	epoch, err := ipcepoch.Current()
	if errors.Is(err, ipcepoch.ErrInvalid) {
		err = ErrPOSIXQueueInvalid
	}
	return POSIXQueueEpoch{BootID: epoch.BootID, NamespaceDevice: epoch.NamespaceDevice, NamespaceInode: epoch.NamespaceInode}, err
}

// ResumePOSIXQueue acquires unlink ownership only for the exact recorded inode
// in its original live boot/IPC namespace. The caller must hold exclusive journal
// ownership. Unknown queues, stale epochs and replacements are never adopted.
func ResumePOSIXQueue(name string, depth int, identity POSIXQueueIdentity, epoch POSIXQueueEpoch) (*POSIXQueue, error) {
	current, err := CurrentPOSIXQueueEpoch()
	if err != nil {
		return nil, err
	}
	if current != epoch || identity.Inode == 0 {
		return nil, ErrPOSIXQueueInvalid
	}
	queue, err := OpenPOSIXQueue(name, depth)
	if err != nil {
		return nil, err
	}
	current, err = CurrentPOSIXQueueEpoch()
	if err != nil || current != epoch || queue.Identity() != identity {
		return nil, errors.Join(ErrPOSIXQueueInvalid, err, queue.Close())
	}
	queue.owned = true
	return queue, nil
}
