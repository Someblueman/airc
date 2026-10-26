package adapter

import (
	"fmt"
	"net"
	"os"
	"testing"
)

func TestSocketPairStreamRejectsAbstractUnixConnection(t *testing.T) {
	for _, name := range []string{"@", fmt.Sprintf("@agw-pair-%d", os.Getpid())} {
		t.Run(name, func(t *testing.T) {
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
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
					t.Fatal("accepted abstract named Unix connection")
				}
			}
		})
	}
}
