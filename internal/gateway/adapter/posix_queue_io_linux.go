//go:build linux && (arm64 || amd64)

package adapter

import (
	"context"
	"errors"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ReadMessage returns a whole queue message. Priority is ordering metadata only.
func (q *POSIXQueue) ReadMessage(ctx context.Context, body []byte) (int, uint32, error) {
	if len(body) < POSIXQueueMessageBytes {
		return 0, 0, ErrPOSIXQueueInvalid
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		n, priority, err := q.receive(body)
		if err == nil {
			return n, priority, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) {
			return 0, 0, err
		}
		if err := q.wait(ctx, unix.POLLIN); err != nil {
			return 0, 0, err
		}
	}
}

// WriteMessage emits one bounded whole message, never a partial suffix.
func (q *POSIXQueue) WriteMessage(ctx context.Context, body []byte) error {
	if len(body) < 1 || len(body) > POSIXQueueMessageBytes {
		return ErrPOSIXQueueInvalid
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := q.send(body)
		if err == nil {
			return nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if err := q.wait(ctx, unix.POLLOUT); err != nil {
			return err
		}
	}
}

func (q *POSIXQueue) receive(body []byte) (int, uint32, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkOpen(); err != nil {
		return 0, 0, err
	}
	var priority uint32
	n, _, errno := unix.Syscall6(unix.SYS_MQ_TIMEDRECEIVE, uintptr(q.fd), uintptr(unsafe.Pointer(&body[0])), uintptr(len(body)), uintptr(unsafe.Pointer(&priority)), 0, 0)
	runtime.KeepAlive(body)
	runtime.KeepAlive(&priority)
	if errno != 0 {
		return 0, 0, errno
	}
	return int(n), priority, nil
}

func (q *POSIXQueue) send(body []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkOpen(); err != nil {
		return err
	}
	_, _, errno := unix.Syscall6(unix.SYS_MQ_TIMEDSEND, uintptr(q.fd), uintptr(unsafe.Pointer(&body[0])), uintptr(len(body)), 0, 0, 0)
	runtime.KeepAlive(body)
	if errno != 0 {
		return errno
	}
	return nil
}

func (q *POSIXQueue) wait(ctx context.Context, events int16) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	milliseconds := 25
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		if remaining < 25*time.Millisecond {
			milliseconds = max(1, int((remaining+time.Millisecond-1)/time.Millisecond))
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkOpen(); err != nil {
		return err
	}
	// Poll and syscalls retain the descriptor lock. Close cannot recycle its FD
	// beneath another operation; the short poll bounds cancellation/close latency.
	descriptors := []unix.PollFd{{Fd: int32(q.fd), Events: events}}
	_, err := unix.Poll(descriptors, milliseconds)
	if errors.Is(err, unix.EINTR) {
		return nil
	}
	if err != nil {
		return err
	}
	if descriptors[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return unix.EIO
	}
	return ctx.Err()
}
