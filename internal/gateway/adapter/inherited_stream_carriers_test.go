//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testfs"
	"golang.org/x/sys/unix"
)

// ownedStream is a carrier stream over duplicates of inherited descriptors.
type ownedStream interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// inheritedStreamCarrier opens a stream over descriptors the process
// inherited; peer is the other end and sources the inherited descriptors.
type inheritedStreamCarrier struct {
	name string
	open func(t *testing.T) (stream ownedStream, peerIn io.Writer, peerOut io.Reader, sources []*os.File)
	// netConn: a second Close reports net.ErrClosed rather than nil.
	netConn bool
}

func inheritedStreamCarriers() []inheritedStreamCarrier {
	return []inheritedStreamCarrier{
		{
			name: "socket-pair", netConn: true,
			open: func(t *testing.T) (ownedStream, io.Writer, io.Reader, []*os.File) {
				source, peer := socketPairFiles(t, unix.SOCK_STREAM)
				stream, err := OpenSocketPairStream(source)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stream.Close() })
				return stream, peer, peer, []*os.File{source}
			},
		},
		{
			name: "inherited-unix", netConn: true,
			open: func(t *testing.T) (ownedStream, io.Writer, io.Reader, []*os.File) {
				root, err := testfs.MkdirTemp("agw-inherited-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(root) })
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: root + "/s", Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				server, err := listener.AcceptUnix()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = server.Close() })
				source, err := server.File()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = source.Close() })
				stream, err := OpenInheritedUnixStream(source)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stream.Close() })
				return stream, client, client, []*os.File{source}
			},
		},
		{
			name: "pipe",
			open: func(t *testing.T) (ownedStream, io.Writer, io.Reader, []*os.File) {
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
				return p, writer, reader, []*os.File{in, out}
			},
		},
	}
}

func socketPairFiles(t *testing.T, kind int) (*os.File, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, kind, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b := os.NewFile(uintptr(fds[0]), "pair-a"), os.NewFile(uintptr(fds[1]), "pair-b")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

// TestInheritedStreamCarriers exercises the owned duplicates of inherited
// stream descriptors once per carrier: deadlines, bytes both ways, bounded
// backpressure, a close that interrupts a blocked read, and inherited
// descriptors that outlive the stream.
func TestInheritedStreamCarriers(t *testing.T) {
	for _, c := range inheritedStreamCarriers() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("deadlines-and-ownership", func(t *testing.T) {
				stream, peerIn, peerOut, sources := c.open(t)
				if err := stream.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				var body [1]byte
				if _, err := stream.Read(body[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("read deadline: %v", err)
				}
				if err := stream.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := peerIn.Write([]byte{'x'}); err != nil {
					t.Fatal(err)
				}
				if n, err := stream.Read(body[:]); n != 1 || err != nil || body[0] != 'x' {
					t.Fatal(n, err, body)
				}
				if err := stream.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := stream.Write([]byte{'y'}); err != nil {
					t.Fatal(err)
				}
				if n, err := peerOut.Read(body[:]); n != 1 || err != nil || body[0] != 'y' {
					t.Fatal(n, err, body)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				for _, source := range sources {
					if _, err := source.Stat(); err != nil {
						t.Fatal("closing the duplicate closed the inherited descriptor", err)
					}
				}
			})
			t.Run("backpressure", func(t *testing.T) {
				stream, _, _, _ := c.open(t)
				if err := stream.SetWriteDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				if n, err := stream.Write(make([]byte, 4<<20)); !errors.Is(err, os.ErrDeadlineExceeded) || n == 4<<20 {
					t.Fatalf("unbounded write: %d, %v", n, err)
				}
			})
			t.Run("close-interrupts-read", func(t *testing.T) {
				stream, _, _, _ := c.open(t)
				if err := stream.SetReadDeadline(time.Time{}); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := stream.Read(make([]byte, 1)); done <- err }()
				time.Sleep(10 * time.Millisecond)
				if err := stream.Close(); err != nil {
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
				if err := stream.Close(); c.netConn != (err != nil) {
					t.Fatal("second close", err)
				}
			})
		})
	}
}
