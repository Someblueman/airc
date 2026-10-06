//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const fifoFirstByteTimeout = 5 * time.Second

// FIFOStream owns a private input.fifo/output.fifo pair and its directory lock.
// The peer must open its return reader before construction. A single reader and
// serialized writer use the stream; filesystem ownership does not authenticate
// frames. Close interrupts both poller reads and first-contact waiting.
type FIFOStream struct {
	pipes             *PipeStream
	root              *os.Root
	lock              *os.File
	closed            chan struct{}
	changed           chan struct{}
	closeOnce         sync.Once
	closeErr          error
	mu                sync.Mutex
	started           bool
	firstByteDeadline time.Time
	readDeadline      time.Time
}

// OpenFIFOStream never self-opens a writer or reader to simulate an absent peer.
// A missing return reader fails immediately; input first contact is bounded.
func OpenFIFOStream(path string) (*FIFOStream, error) {
	root, lock, err := openPrivateDirectory(path)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
			_ = root.Close()
		}
	}()
	in, err := openPrivateFIFO(root, "input.fifo", os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := openPrivateFIFO(root, "output.fifo", os.O_WRONLY)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	inputInfo, err := in.Stat()
	if err != nil {
		return nil, err
	}
	outputInfo, err := out.Stat()
	if err != nil {
		return nil, err
	}
	if os.SameFile(inputInfo, outputInfo) {
		return nil, errors.New("FIFO pair aliases one node")
	}
	// Recheck both paths after acquiring both endpoints, before journal ownership.
	for name, before := range map[string]os.FileInfo{"input.fifo": inputInfo, "output.fifo": outputInfo} {
		after, err := root.Lstat(name)
		if err != nil || !privateFIFO(after) || !os.SameFile(before, after) {
			return nil, errors.New("FIFO path changed during open")
		}
	}
	current, err := os.Lstat(path)
	locked, lockErr := lock.Stat()
	if err != nil || lockErr != nil || !os.SameFile(current, locked) || current.Mode().Perm() != 0700 || !unixPathOwner(current) {
		return nil, errors.New("FIFO root changed during open")
	}
	pipes, err := OpenPipeStream(in, out)
	if err != nil {
		return nil, err
	}
	f := &FIFOStream{pipes: pipes, root: root, lock: lock, closed: make(chan struct{}), changed: make(chan struct{}, 1), firstByteDeadline: time.Now().Add(fifoFirstByteTimeout)}
	success = true
	return f, nil
}

func privateFIFO(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return info.Mode()&os.ModeNamedPipe != 0 && info.Mode().Perm() == 0600 && unixPathOwner(info) && ok && stat.Nlink == 1
}

func openPrivateFIFO(root *os.Root, name string, flags int) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !privateFIFO(before) {
		return nil, errors.New("FIFO requires an owner-private single-link node")
	}
	f, err := root.OpenFile(name, flags|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !privateFIFO(after) || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, errors.New("FIFO node changed during open")
	}
	return f, nil
}

func (f *FIFOStream) effectiveReadDeadline() time.Time {
	if !f.started && (f.readDeadline.IsZero() || f.firstByteDeadline.Before(f.readDeadline)) {
		return f.firstByteDeadline
	}
	return f.readDeadline
}

func (f *FIFOStream) SetReadDeadline(t time.Time) error {
	select {
	case <-f.closed:
		return os.ErrClosed
	default:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readDeadline = t
	select {
	case f.changed <- struct{}{}:
	default:
	}
	return nil
}

func (f *FIFOStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	raw, err := f.pipes.input.SyscallConn()
	if err != nil {
		select {
		case <-f.closed:
			return 0, os.ErrClosed
		default:
			return 0, err
		}
	}
	for {
		select {
		case <-f.closed:
			return 0, os.ErrClosed
		default:
		}
		f.mu.Lock()
		deadline := f.effectiveReadDeadline()
		f.mu.Unlock()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		var n int
		var readErr error
		if err := raw.Control(func(fd uintptr) { n, readErr = unix.Read(int(fd), b) }); err != nil {
			select {
			case <-f.closed:
				return 0, os.ErrClosed
			default:
				return 0, err
			}
		}
		f.mu.Lock()
		if n > 0 {
			f.started = true
			f.mu.Unlock()
			return n, readErr
		}
		started, deadline := f.started, f.effectiveReadDeadline()
		f.mu.Unlock()
		if readErr == nil && started {
			return 0, io.EOF
		}
		if readErr != nil && !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EINTR) {
			return 0, readErr
		}
		// Darwin's FIFO poller does not reliably wake a blocked read when its
		// writer closes. Inspect the nonblocking descriptor between bounded,
		// interruptible waits, both before first contact and after EAGAIN.
		wait := 10 * time.Millisecond
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			if remaining < wait {
				wait = remaining
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-f.closed:
			timer.Stop()
			return 0, os.ErrClosed
		case <-f.changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (f *FIFOStream) Write(b []byte) (int, error)        { return f.pipes.Write(b) }
func (f *FIFOStream) SetWriteDeadline(t time.Time) error { return f.pipes.SetWriteDeadline(t) }
func (f *FIFOStream) Close() error {
	f.closeOnce.Do(func() { close(f.closed); f.closeErr = errors.Join(f.pipes.Close(), f.lock.Close(), f.root.Close()) })
	return f.closeErr
}
