//go:build darwin || linux

package adapter

import (
	"net"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/testfs"
	"golang.org/x/sys/unix"
)

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
			for mode, open := range map[string]func(*os.File) (net.Conn, error){"socketpair": OpenSocketPairStream, "inherited": OpenInheritedUnixStream} {
				t.Run(mode, func(t *testing.T) {
					conn, err := open(source)
					if conn != nil {
						conn.Close()
					}
					if err == nil {
						t.Fatal("accepted incompatible descriptor")
					}
				})
			}
		})
	}
}

func TestSocketPairStreamRejectsNamedUnixConnection(t *testing.T) {
	root, err := testfs.MkdirTemp("agw-pair-")
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
