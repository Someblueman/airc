//go:build (darwin && arm64) || (linux && arm64)

package adapter

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/Someblueman/airc/internal/gateway/sharedmem"
	"golang.org/x/sys/unix"
)

const sharedQueueLockName = "transaction.lock"
const sharedQueueRetry = 5 * time.Millisecond

var sharedQueueMagic = [4]byte{'A', 'G', 'Q', '1'}

func OpenSharedMemoryQueue(name, directory string, consumer bool) (*SharedMemoryQueue, error) {
	if !ValidSharedMemoryName(name) {
		return nil, ErrSharedMemoryInvalid
	}
	root, dirAnchor, err := openPrivateDirectoryRoot(directory)
	if err != nil {
		return nil, err
	}
	q := &SharedMemoryQueue{
		name: name, directory: directory, consumer: consumer,
		root: root, dirAnchor: dirAnchor, dirFD: int(dirAnchor.Fd()),
		operations: make(chan struct{}, 1), closed: make(chan struct{}),
	}
	fail := func(err error) (*SharedMemoryQueue, error) { return nil, errors.Join(err, q.closeResources()) }
	if consumer {
		if err := unix.Flock(q.dirFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			return fail(fmt.Errorf("shared-memory consumer directory lock: %w", err))
		}
	}
	lockInfo, err := root.Lstat(sharedQueueLockName)
	if err != nil || !privateTransactionLock(lockInfo) {
		if err == nil {
			err = ErrSharedMemoryInvalid
		}
		return fail(fmt.Errorf("shared-memory transaction lock: %w", err))
	}
	q.lockFile, err = root.OpenFile(sharedQueueLockName, os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(fmt.Errorf("open shared-memory transaction lock: %w", err))
	}
	q.lockFD = int(q.lockFile.Fd())
	openedInfo, err := q.lockFile.Stat()
	if err != nil || !privateTransactionLock(openedInfo) || !os.SameFile(lockInfo, openedInfo) {
		if err == nil {
			err = ErrSharedMemoryInvalid
		}
		return fail(fmt.Errorf("shared-memory transaction lock changed during open: %w", err))
	}
	q.mapping, err = sharedmem.OpenExisting(name, sharedMemoryBytes)
	if err != nil {
		return fail(fmt.Errorf("open shared-memory queue: %w", err))
	}
	q.view = q.mapping.Bytes()
	if len(q.view) != sharedMemoryBytes || uintptr(unsafe.Pointer(&q.view[0]))%unsafe.Alignof(uint32(0)) != 0 {
		return fail(ErrSharedMemoryInvalid)
	}
	if err := q.verifyHandles(); err != nil {
		return fail(err)
	}
	if err := q.mapping.Validate(); err != nil {
		return fail(err)
	}
	return q, nil
}

func privateTransactionLock(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint32(stat.Uid) == uint32(os.Geteuid()) && stat.Nlink == 1 && uint32(stat.Mode)&07777 == 0600
}

func (q *SharedMemoryQueue) verifyHandles() error {
	currentDir, err := os.Lstat(q.directory)
	if err != nil {
		return fmt.Errorf("shared-memory directory: %w", err)
	}
	openedDir, err := q.dirAnchor.Stat()
	if err != nil || !openedDir.IsDir() || currentDir.Mode()&os.ModeSymlink != 0 || currentDir.Mode().Perm() != 0700 || !unixPathOwner(currentDir) || !os.SameFile(currentDir, openedDir) {
		if err == nil {
			err = ErrSharedMemoryInvalid
		}
		return fmt.Errorf("shared-memory directory changed: %w", err)
	}
	currentLock, err := q.root.Lstat(sharedQueueLockName)
	if err != nil {
		return fmt.Errorf("shared-memory transaction lock path: %w", err)
	}
	openedLock, err := q.lockFile.Stat()
	if err != nil || !privateTransactionLock(currentLock) || !privateTransactionLock(openedLock) || !os.SameFile(currentLock, openedLock) {
		if err == nil {
			err = ErrSharedMemoryInvalid
		}
		return fmt.Errorf("shared-memory transaction lock changed: %w", err)
	}
	if len(q.view) != sharedMemoryBytes {
		return ErrSharedMemoryInvalid
	}
	return nil
}

func (q *SharedMemoryQueue) enter(ctx context.Context) error {
	if q == nil {
		return os.ErrClosed
	}
	if q.closed == nil || q.operations == nil {
		return ErrSharedMemoryInvalid
	}
	if ctx == nil {
		return ErrSharedMemoryInvalid
	}
	select {
	case <-q.closed:
		return os.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case q.operations <- struct{}{}:
	}
	q.mu.Lock()
	if err := q.checkOpen(); err != nil {
		q.mu.Unlock()
		<-q.operations
		return err
	}
	return nil
}

func (q *SharedMemoryQueue) leave() {
	q.mu.Unlock()
	<-q.operations
}

func (q *SharedMemoryQueue) checkOpen() error {
	select {
	case <-q.closed:
		return os.ErrClosed
	default:
	}
	return q.verifyHandles()
}

func (q *SharedMemoryQueue) acquireTransaction(ctx context.Context) error {
	for {
		if err := q.checkOpen(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(q.lockFD, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			if err := q.checkOpen(); err != nil {
				return errors.Join(err, unix.Flock(q.lockFD, unix.LOCK_UN))
			}
			if err := q.mapping.Validate(); err != nil {
				return errors.Join(err, unix.Flock(q.lockFD, unix.LOCK_UN))
			}
			if err := ctx.Err(); err != nil {
				return errors.Join(err, unix.Flock(q.lockFD, unix.LOCK_UN))
			}
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return err
		}
		if err := waitSharedMemoryQueue(ctx, q.closed); err != nil {
			return err
		}
	}
}

func waitSharedMemoryQueue(ctx context.Context, closed <-chan struct{}) error {
	timer := time.NewTimer(sharedQueueRetry)
	defer timer.Stop()
	select {
	case <-closed:
		return os.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (q *SharedMemoryQueue) ReadMessage(ctx context.Context, body []byte) (int, error) {
	if q == nil || !q.consumer {
		return 0, errSharedMemoryQueueSide
	}
	for {
		if err := q.enter(ctx); err != nil {
			return 0, err
		}
		if err := q.acquireTransaction(ctx); err != nil {
			q.leave()
			return 0, err
		}
		n, ready, opErr := q.readLocked(ctx, body)
		unlockErr := unix.Flock(q.lockFD, unix.LOCK_UN)
		q.leave()
		if err := errors.Join(opErr, unlockErr); err != nil {
			return 0, err
		}
		if ready {
			return n, nil
		}
		if err := waitSharedMemoryQueue(ctx, q.closed); err != nil {
			return 0, err
		}
	}
}

func (q *SharedMemoryQueue) readLocked(ctx context.Context, body []byte) (int, bool, error) {
	if !q.validStaticHeader() {
		return 0, false, ErrSharedMemoryInvalid
	}
	state := atomic.LoadUint32((*uint32)(unsafe.Pointer(&q.view[0])))
	switch state {
	case sharedStateEmpty:
		if binary.LittleEndian.Uint32(q.view[8:12]) != 0 {
			return 0, false, ErrSharedMemoryInvalid
		}
		return 0, false, nil
	case sharedStateWriting:
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		q.resetSlot()
		return 0, false, nil
	case sharedStateReady:
		length, ok := q.readyLength()
		if !ok {
			return 0, false, ErrSharedMemoryInvalid
		}
		if len(body) < length {
			return 0, false, errSharedMemoryQueueBuffer
		}
		copy(body[:length], q.view[sharedSlotHeader:sharedSlotHeader+length])
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		q.resetSlot()
		return length, true, nil
	default:
		return 0, false, ErrSharedMemoryInvalid
	}
}

func (q *SharedMemoryQueue) WriteMessage(ctx context.Context, body []byte) error {
	if q == nil || q.consumer {
		return errSharedMemoryQueueSide
	}
	if len(body) < 1 || len(body) > sharedSlotBody {
		return ErrSharedMemoryInvalid
	}
	message := append([]byte(nil), body...)
	for {
		if err := q.enter(ctx); err != nil {
			return err
		}
		if err := q.acquireTransaction(ctx); err != nil {
			q.leave()
			return err
		}
		written, opErr := q.writeLocked(ctx, message)
		unlockErr := unix.Flock(q.lockFD, unix.LOCK_UN)
		q.leave()
		if err := errors.Join(opErr, unlockErr); err != nil {
			return err
		}
		if written {
			return nil
		}
		if err := waitSharedMemoryQueue(ctx, q.closed); err != nil {
			return err
		}
	}
}

func (q *SharedMemoryQueue) writeLocked(ctx context.Context, body []byte) (bool, error) {
	if !q.validStaticHeader() {
		return false, ErrSharedMemoryInvalid
	}
	state := atomic.LoadUint32((*uint32)(unsafe.Pointer(&q.view[0])))
	switch state {
	case sharedStateReady:
		if _, ok := q.readyLength(); !ok {
			return false, ErrSharedMemoryInvalid
		}
		return false, nil
	case sharedStateEmpty:
		if binary.LittleEndian.Uint32(q.view[8:12]) != 0 {
			return false, ErrSharedMemoryInvalid
		}
	case sharedStateWriting:
		// Static header validation above succeeded; the previous producer may
		// have crashed while changing length or body.
	default:
		return false, ErrSharedMemoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&q.view[0])), sharedStateWriting)
	binary.LittleEndian.PutUint32(q.view[8:12], uint32(len(body)))
	copy(q.view[sharedSlotHeader:sharedSlotHeader+len(body)], body)
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&q.view[0])), sharedStateReady)
	return true, nil
}

func (q *SharedMemoryQueue) validStaticHeader() bool {
	return string(q.view[4:8]) == string(sharedQueueMagic[:]) && zeroBytes(q.view[12:16])
}

func (q *SharedMemoryQueue) readyLength() (int, bool) {
	length := binary.LittleEndian.Uint32(q.view[8:12])
	if !q.validStaticHeader() || length == 0 || length > sharedSlotBody {
		return 0, false
	}
	return int(length), true
}

func (q *SharedMemoryQueue) resetSlot() {
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&q.view[0])), sharedStateWriting)
	binary.LittleEndian.PutUint32(q.view[8:12], 0)
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&q.view[0])), sharedStateEmpty)
}

func zeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func (q *SharedMemoryQueue) closeResources() error {
	var errs []error
	if q.mapping != nil {
		errs = append(errs, q.mapping.Close())
		q.mapping = nil
		q.view = nil
	}
	if q.lockFile != nil {
		errs = append(errs, q.lockFile.Close())
		q.lockFile = nil
	}
	if q.dirAnchor != nil {
		errs = append(errs, q.dirAnchor.Close())
		q.dirAnchor = nil
	}
	if q.root != nil {
		errs = append(errs, q.root.Close())
		q.root = nil
	}
	return errors.Join(errs...)
}

func (q *SharedMemoryQueue) Close() error {
	if q == nil || q.closed == nil {
		return nil
	}
	q.closeOnce.Do(func() {
		close(q.closed)
		q.mu.Lock()
		q.closeErr = q.closeResources()
		q.mu.Unlock()
	})
	return q.closeErr
}
