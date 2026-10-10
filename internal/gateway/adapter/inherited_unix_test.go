//go:build darwin || linux

package adapter

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testfs"
	"golang.org/x/sys/unix"
)

func TestInheritedUnixStreamNamedEndpointOwnershipAndDeadline(t *testing.T) {
	root, err := testfs.MkdirTemp("agw-inherited-")
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
	source, err := server.File()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	stream, err := OpenInheritedUnixStream(source)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var body [1]byte
	if _, err := stream.Read(body[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("missing read deadline", err)
	}
	if err := stream.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte{'x'}); err != nil {
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
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Read(body[:]); n != 1 || err != nil || body[0] != 'y' {
		t.Fatal(n, err, body)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Stat(); err != nil {
		t.Fatal("duplicate close closed source", err)
	}
}

func TestInheritedUnixStreamRejectsUnconnectedDescriptor(t *testing.T) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	source := os.NewFile(uintptr(fd), "unconnected")
	defer source.Close()
	stream, err := OpenInheritedUnixStream(source)
	if stream != nil {
		stream.Close()
	}
	if err == nil {
		t.Fatal("accepted unconnected Unix endpoint")
	}
}
