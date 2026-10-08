//go:build (darwin || linux) && arm64

package sysvqueue

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// Queue owns a key/identifier lifecycle, not a file descriptor. Operations
// verify kernel metadata before using the ID. Application authentication is
// required even when the OS owner matches. Callers must join users before Close.
type Queue struct {
	mu                         sync.Mutex
	identity                   Identity
	owned, initialized, closed bool
	closeErr                   error
}

func Create(key int32) (*Queue, error) { return open(key, true) }
func Open(key int32) (*Queue, error)   { return open(key, false) }
func open(key int32, create bool) (*Queue, error) {
	if key <= 0 {
		return nil, ErrInvalid
	}
	flags := uintptr(0600)
	if create {
		flags |= unix.IPC_CREAT | unix.IPC_EXCL
	}
	id, _, errno := nativeGet(uintptr(key), flags)
	if errno != 0 {
		return nil, errno
	}
	q := &Queue{identity: Identity{ID: int(id), Key: key}, owned: create, initialized: true}
	identity, err := q.inspect()
	if err != nil {
		if create {
			_, _, cleanup := nativeControl(id, unix.IPC_RMID, 0)
			if cleanup != 0 {
				err = errors.Join(err, cleanup)
			}
		}
		return nil, err
	}
	q.identity = identity
	return q, nil
}
func (q *Queue) Identity() Identity { return q.identity }

func (q *Queue) check() error {
	if !q.initialized {
		return ErrInvalid
	}
	if q.closed {
		return net.ErrClosed
	}
	identity, err := q.inspect()
	if err != nil {
		return err
	}
	if identity != q.identity {
		return ErrInvalid
	}
	return nil
}
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.initialized {
		return ErrInvalid
	}
	if q.closed {
		return q.closeErr
	}
	if q.owned {
		if err := q.check(); err != nil {
			q.closeErr = err
		} else {
			_, _, errno := nativeControl(uintptr(q.identity.ID), unix.IPC_RMID, 0)
			if errno != 0 {
				q.closeErr = errno
			}
		}
	}
	q.closed = true
	return q.closeErr
}

type packet struct {
	Type int64
	Body [MessageBytes]byte
}

// ReadMessage never enables MSG_NOERROR: oversized kernel records are refused
// without truncation or consumption. Successful receive is destructive, not admission.
func (q *Queue) ReadMessage(ctx context.Context, body []byte) (int, error) {
	if ctx == nil || len(body) < MessageBytes {
		return 0, ErrInvalid
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var p packet
		q.mu.Lock()
		err := q.check()
		var n uintptr
		if err == nil {
			var errno unix.Errno
			n, _, errno = nativeReceive(uintptr(q.identity.ID), uintptr(unsafe.Pointer(&p)), MessageBytes)
			runtime.KeepAlive(&p)
			if errno != 0 {
				err = errno
			}
		}
		q.mu.Unlock()
		if err == nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if p.Type != 1 || n < 1 || n > MessageBytes {
				return 0, ErrInvalid
			}
			copy(body, p.Body[:int(n)])
			return int(n), nil
		}
		if !errors.Is(err, unix.ENOMSG) && !errors.Is(err, unix.EINTR) {
			return 0, err
		}
		if err := wait(ctx); err != nil {
			return 0, err
		}
	}
}
func (q *Queue) WriteMessage(ctx context.Context, body []byte) error {
	if ctx == nil || len(body) < 1 || len(body) > MessageBytes {
		return ErrInvalid
	}
	p := packet{Type: 1}
	copy(p.Body[:], body)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		q.mu.Lock()
		err := q.check()
		if err == nil {
			_, _, errno := nativeSend(uintptr(q.identity.ID), uintptr(unsafe.Pointer(&p)), uintptr(len(body)))
			runtime.KeepAlive(&p)
			if errno != 0 {
				err = errno
			}
		}
		q.mu.Unlock()
		if err == nil {
			return ctx.Err()
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return err
		}
		if err := wait(ctx); err != nil {
			return err
		}
	}
}
func wait(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
