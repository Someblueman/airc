//go:build darwin || linux

package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func pipeFixture(t *testing.T) (*PipeStream, *os.File, *os.File) {
	t.Helper()
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = writer.Close(); _ = reader.Close(); _ = out.Close() })
	p, err := OpenPipeStream(in, out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	raw, err := p.input.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var inspectErr error
	if err = raw.Control(func(fd uintptr) { flags, inspectErr = unix.FcntlInt(fd, unix.F_GETFD, 0) }); err == nil {
		err = inspectErr
	}
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("duplicate is not close-on-exec: %v, %d", err, flags)
	}
	return p, writer, reader
}

func TestPipeStreamDeadlineAndClose(t *testing.T) {
	p, _, _ := pipeFixture(t)
	if err := p.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read deadline: %v", err)
	}
	if err := p.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := p.Read(make([]byte, 1)); done <- err }()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not interrupt read")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPipeStreamBackpressure(t *testing.T) {
	p, _, _ := pipeFixture(t)
	if err := p.SetWriteDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	n, err := p.Write(make([]byte, 4<<20))
	if !errors.Is(err, os.ErrDeadlineExceeded) || n == 4<<20 {
		t.Fatalf("unbounded pipe write: %d, %v", n, err)
	}
}

func TestPipeStreamRejectsNonPipesAndWrongDirection(t *testing.T) {
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer writer.Close()
	file, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fifoPath := filepath.Join(t.TempDir(), "duplex-fifo")
	if err := unix.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal(err)
	}
	duplex, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer duplex.Close()
	for _, pair := range [][2]*os.File{{file, writer}, {in, file}, {writer, writer}, {in, in}, {nil, writer}, {duplex, writer}, {in, duplex}} {
		if p, err := OpenPipeStream(pair[0], pair[1]); err == nil {
			_ = p.Close()
			t.Fatal("invalid descriptors accepted")
		}
	}
}
