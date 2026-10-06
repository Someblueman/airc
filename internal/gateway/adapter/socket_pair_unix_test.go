//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

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

func TestSocketPairStreamOwnershipAndDeadlines(t *testing.T) {
	source, peer := socketPairFiles(t, unix.SOCK_STREAM)
	stream, err := OpenSocketPairStream(source)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
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
	if _, err := peer.Write([]byte{'x'}); err != nil {
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
	if n, err := peer.Read(body[:]); n != 1 || err != nil || body[0] != 'y' {
		t.Fatal(n, err, body)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Stat(); err != nil {
		t.Fatal("duplicate close closed source", err)
	}
}

func TestSocketPairStreamRejectsOtherDescriptors(t *testing.T) {
	datagram, _ := socketPairFiles(t, unix.SOCK_DGRAM)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	regular, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connected, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connected.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	tcp, err := connected.(*net.TCPConn).File()
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	for name, source := range map[string]*os.File{"nil": nil, "datagram": datagram, "pipe": reader, "regular": regular, "tcp": tcp} {
		t.Run(name, func(t *testing.T) {
			conn, err := OpenSocketPairStream(source)
			if conn != nil {
				conn.Close()
			}
			if err == nil {
				t.Fatal("accepted non-pair descriptor")
			}
		})
	}
}

func TestSocketPairStreamRejectsNamedUnixConnection(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "agw-pair-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: root + "/s", Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, conn := range []*net.UnixConn{client, server} {
		descriptor, err := conn.File()
		if err != nil {
			t.Fatal(err)
		}
		stream, err := OpenSocketPairStream(descriptor)
		descriptor.Close()
		if stream != nil {
			stream.Close()
		}
		if err == nil {
			t.Fatal("accepted named Unix connection")
		}
	}
}
