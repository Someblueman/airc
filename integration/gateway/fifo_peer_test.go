//go:build darwin || linux

package gateway_test

import (
	"errors"
	"io"
	"os"
	"time"

	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// The producer must bound initial return contact too: FIFO EOF before the child
// opens its writer is not established-child shutdown. After a byte, normal EOF
// applies. This peer has one reader; its deadlines are set by that reader.
type fifoPeer struct {
	input, output *os.File
	childDone     <-chan struct{}
	closeOnce     sync.Once
	closeErr      error
	started       bool
	readDeadline  time.Time
}

func (p *fifoPeer) SetReadDeadline(t time.Time) error {
	p.readDeadline = t
	return p.input.SetReadDeadline(t)
}
func (p *fifoPeer) Read(b []byte) (int, error) {
	for {
		n, err := p.input.Read(b)
		if n > 0 {
			p.started = true
		}
		if p.started || !errors.Is(err, io.EOF) || len(b) == 0 {
			return n, err
		}
		if p.readDeadline.IsZero() || !time.Now().Before(p.readDeadline) {
			return 0, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
}

func newFIFOPeer(t *testing.T, input, output *os.File) *fifoPeer {
	t.Helper()
	duplicate := func(source *os.File) *os.File {
		raw, err := source.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		fd := -1
		var duplicateErr error
		if err := raw.Control(func(sourceFD uintptr) { fd, duplicateErr = unix.FcntlInt(sourceFD, unix.F_DUPFD_CLOEXEC, 3) }); err != nil {
			t.Fatal(err)
		}
		if duplicateErr != nil {
			t.Fatal(duplicateErr)
		}
		file := os.NewFile(uintptr(fd), "fifo-peer")
		if file == nil {
			_ = unix.Close(fd)
			t.Fatal("invalid FIFO peer duplicate")
		}
		t.Cleanup(func() { _ = file.Close() })
		if err := file.SetDeadline(time.Time{}); err != nil {
			t.Fatal(err)
		}
		return file
	}
	return &fifoPeer{input: duplicate(input), output: duplicate(output)}
}

func (p *fifoPeer) Write(b []byte) (int, error)        { return p.output.Write(b) }
func (p *fifoPeer) SetWriteDeadline(t time.Time) error { return p.output.SetWriteDeadline(t) }
func (p *fifoPeer) CloseWrite() error                  { return p.output.Close() }
func (p *fifoPeer) Close() error {
	p.closeOnce.Do(func() { p.closeErr = errors.Join(p.input.Close(), p.output.Close()) })
	return p.closeErr
}
