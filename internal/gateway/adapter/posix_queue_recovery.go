package adapter

// POSIXQueueEpoch scopes a queue inode to the live Linux boot and IPC namespace.
// It is ownership bookkeeping, never producer authentication. Recovery callers
// must load it and the queue identity from their exclusively locked journal.
type POSIXQueueEpoch struct {
	BootID                          string
	NamespaceDevice, NamespaceInode uint64
}
