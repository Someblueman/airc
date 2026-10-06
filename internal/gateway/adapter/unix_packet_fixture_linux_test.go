//go:build linux

package adapter

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func unixPacketPath(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "up-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "socket")
}

func unixPacketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	path := unixPacketPath(t)
	listener, err := ListenUnixPacket(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	client, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Net: "unixpacket", Name: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	server, ok := accepted.(*net.UnixConn)
	if !ok {
		_ = accepted.Close()
		t.Fatal("record listener accepted non-Unix connection")
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, client
}
