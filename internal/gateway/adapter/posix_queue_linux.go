//go:build linux && (arm64 || amd64)

package adapter

import (
	"errors"
	"net"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

type posixQueueAttributes struct {
	Flags, MaxMessages, MessageSize, CurrentMessages int64
	Reserved                                         [4]int64
}

// POSIXQueue owns a nonblocking kernel descriptor and a pinned mqueue directory.
// Exclusive creation owns the name; opening a producer/response descriptor does not.
type POSIXQueue struct {
	mu            sync.Mutex
	fd, directory int
	name          string
	identity      POSIXQueueIdentity
	owned, closed bool
	initialized   bool
	closeErr      error
}

func CreatePOSIXQueue(name string, depth int) (*POSIXQueue, error) {
	return openPOSIXQueue(name, depth, true)
}
func OpenPOSIXQueue(name string, depth int) (*POSIXQueue, error) {
	return openPOSIXQueue(name, depth, false)
}
func (q *POSIXQueue) Identity() POSIXQueueIdentity { return q.identity }

func openPOSIXQueue(name string, depth int, create bool) (*POSIXQueue, error) {
	if !ValidPOSIXQueueName(name) || depth < 1 || depth > POSIXQueueMaxDepth {
		return nil, ErrPOSIXQueueInvalid
	}
	directory, err := unix.Open("/dev/mqueue", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(directory, &fs); err != nil || fs.Type != 0x19800202 /* Linux ipc/mqueue.c: MQUEUE_MAGIC */ {
		_ = unix.Close(directory)
		if err != nil {
			return nil, err
		}
		return nil, ErrPOSIXQueueInvalid
	}
	pointer, err := unix.BytePtrFromString(name[1:])
	if err != nil {
		_ = unix.Close(directory)
		return nil, err
	}
	attributes := posixQueueAttributes{MaxMessages: int64(depth), MessageSize: POSIXQueueMessageBytes}
	flags := unix.O_RDWR | unix.O_NONBLOCK | unix.O_CLOEXEC
	if create {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, _, errno := unix.Syscall6(unix.SYS_MQ_OPEN, uintptr(unsafe.Pointer(pointer)), uintptr(flags), 0600, uintptr(unsafe.Pointer(&attributes)), 0, 0)
	runtime.KeepAlive(pointer)
	runtime.KeepAlive(&attributes)
	if errno != 0 {
		_ = unix.Close(directory)
		return nil, errno
	}
	q := &POSIXQueue{fd: int(fd), directory: directory, name: name, owned: create, initialized: true}
	var stat unix.Stat_t
	if err := unix.Fstat(q.fd, &stat); err != nil {
		return nil, errors.Join(err, q.Close())
	}
	q.identity = POSIXQueueIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&07777 != 0600 {
		return nil, errors.Join(ErrPOSIXQueueInvalid, q.Close())
	}
	if _, err := q.sameNameIdentity(); err != nil {
		return nil, errors.Join(err, q.Close())
	}
	var actual posixQueueAttributes
	_, _, errno = unix.Syscall(unix.SYS_MQ_GETSETATTR, uintptr(q.fd), 0, uintptr(unsafe.Pointer(&actual)))
	runtime.KeepAlive(&actual)
	if errno != 0 {
		return nil, errors.Join(errno, q.Close())
	}
	if actual.Flags&unix.O_NONBLOCK == 0 || actual.MaxMessages != int64(depth) || actual.MessageSize != POSIXQueueMessageBytes || actual.CurrentMessages < 0 || actual.CurrentMessages > actual.MaxMessages {
		return nil, errors.Join(ErrPOSIXQueueInvalid, q.Close())
	}
	return q, nil
}

// verifyName requires the configured name still to identify the pinned descriptor.
// The caller holds mu after publication, preventing Close and FD reuse mid-check.
func (q *POSIXQueue) sameNameIdentity() (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(q.directory, q.name[1:], &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return stat, err
	}
	if uint64(stat.Dev) != q.identity.Device || stat.Ino != q.identity.Inode {
		return stat, ErrPOSIXQueueInvalid
	}
	return stat, nil
}

func (q *POSIXQueue) verifyName() error {
	stat, err := q.sameNameIdentity()
	if err != nil {
		return err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&07777 != 0600 {
		return ErrPOSIXQueueInvalid
	}
	return nil
}

func (q *POSIXQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.initialized {
		return ErrPOSIXQueueInvalid
	}
	if q.closed {
		return q.closeErr
	}
	q.closed = true
	if q.owned {
		_, err := q.sameNameIdentity()
		if err == nil {
			err = unix.Unlinkat(q.directory, q.name[1:], 0)
		}
		if !errors.Is(err, unix.ENOENT) {
			q.closeErr = err
		}
	}
	q.closeErr = errors.Join(q.closeErr, unix.Close(q.fd), unix.Close(q.directory))
	return q.closeErr
}

func (q *POSIXQueue) checkOpen() error {
	if !q.initialized {
		return ErrPOSIXQueueInvalid
	}
	if q.closed {
		return net.ErrClosed
	}
	return q.verifyName()
}
